// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"

	"github.com/gpuaudit/cli/internal/analysis"
	"github.com/gpuaudit/cli/internal/diff"
	"github.com/gpuaudit/cli/internal/models"
	"github.com/gpuaudit/cli/internal/output"
	"github.com/gpuaudit/cli/internal/pricing"
	awsprovider "github.com/gpuaudit/cli/internal/providers/aws"
	k8sprovider "github.com/gpuaudit/cli/internal/providers/k8s"
)

var version = "dev"

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "gpuaudit",
	Short: "Scan your cloud accounts for GPU waste",
	Long:  `gpuaudit scans your AWS account for GPU instances, analyzes their utilization, and identifies waste with specific recommendations to reduce your GPU cloud spend.`,
}

// --- scan command ---

var (
	scanProfile       string
	scanRegions       []string
	scanFormat        string
	scanOutput        string
	scanSkipMetrics   bool
	scanSkipSageMaker bool
	scanSkipEKS       bool
	scanSkipK8s       bool
	scanSkipCosts     bool
	scanKubeconfig    string
	scanKubeContext   string
	scanPromURL       string
	scanPromEndpoint  string
	scanExcludeTags   []string
	scanMinUptimeDays int
	scanTargets       []string
	scanRole          string
	scanExternalID    string
	scanOrg           bool
	scanSkipSelf      bool
)

// --- diff command ---

var diffFormat string

var diffCmd = &cobra.Command{
	Use:   "diff <old.json> <new.json>",
	Short: "Compare two scan results and show what changed",
	Args:  cobra.ExactArgs(2),
	RunE:  runDiff,
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan AWS account for GPU waste",
	RunE:  runScan,
}

func init() {
	scanCmd.Flags().StringVar(&scanProfile, "profile", "", "AWS profile to use")
	scanCmd.Flags().StringSliceVar(&scanRegions, "region", nil, "AWS regions to scan (default: common GPU regions)")
	scanCmd.Flags().StringVar(&scanFormat, "format", "table", "Output format: table, json, markdown, slack")
	scanCmd.Flags().StringVarP(&scanOutput, "output", "o", "", "Write output to file instead of stdout")
	scanCmd.Flags().BoolVar(&scanSkipMetrics, "skip-metrics", false, "Skip CloudWatch metrics collection (faster but less accurate)")
	scanCmd.Flags().BoolVar(&scanSkipSageMaker, "skip-sagemaker", false, "Skip SageMaker endpoint scanning")
	scanCmd.Flags().BoolVar(&scanSkipEKS, "skip-eks", false, "Skip EKS GPU node group scanning")
	scanCmd.Flags().BoolVar(&scanSkipK8s, "skip-k8s", false, "Skip Kubernetes API GPU node scanning")
	scanCmd.Flags().BoolVar(&scanSkipCosts, "skip-costs", false, "Skip Cost Explorer data enrichment")
	scanCmd.Flags().StringVar(&scanKubeconfig, "kubeconfig", "", "Path to kubeconfig file (default: ~/.kube/config)")
	scanCmd.Flags().StringVar(&scanKubeContext, "kube-context", "", "Kubernetes context to use (default: current context)")
	scanCmd.Flags().StringVar(&scanPromURL, "prom-url", "", "Prometheus URL for GPU metrics (e.g., https://prometheus.corp.example.com)")
	scanCmd.Flags().StringVar(&scanPromEndpoint, "prom-endpoint", "", "In-cluster Prometheus service as namespace/service:port (e.g., monitoring/prometheus:9090)")
	scanCmd.Flags().StringSliceVar(&scanExcludeTags, "exclude-tag", nil, "Exclude instances matching tag (key=value, repeatable)")
	scanCmd.Flags().IntVar(&scanMinUptimeDays, "min-uptime-days", 0, "Only flag instances running for at least this many days")
	scanCmd.Flags().StringSliceVar(&scanTargets, "targets", nil, "Account IDs to scan (comma-separated)")
	scanCmd.Flags().StringVar(&scanRole, "role", "", "IAM role name to assume in each target")
	scanCmd.Flags().StringVar(&scanExternalID, "external-id", "", "STS external ID for cross-account role assumption")
	scanCmd.Flags().BoolVar(&scanOrg, "org", false, "Auto-discover all accounts from AWS Organizations")
	scanCmd.Flags().BoolVar(&scanSkipSelf, "skip-self", false, "Exclude the caller's own account from the scan")
	scanCmd.MarkFlagsMutuallyExclusive("targets", "org")

	diffCmd.Flags().StringVar(&diffFormat, "format", "table", "Output format: table, json")

	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(diffCmd)
	rootCmd.AddCommand(pricingCmd)
	rootCmd.AddCommand(iamPolicyCmd)
	rootCmd.AddCommand(versionCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	if scanPromURL != "" && scanPromEndpoint != "" {
		return fmt.Errorf("--prom-url and --prom-endpoint are mutually exclusive")
	}

	ctx := context.Background()

	if (len(scanTargets) > 0 || scanOrg) && scanRole == "" {
		return fmt.Errorf("--role is required when using --targets or --org")
	}

	opts := awsprovider.DefaultScanOptions()
	opts.Profile = scanProfile
	opts.Regions = scanRegions
	opts.SkipMetrics = scanSkipMetrics
	opts.SkipSageMaker = scanSkipSageMaker
	opts.SkipEKS = scanSkipEKS
	opts.SkipCosts = scanSkipCosts
	opts.ExcludeTags = parseExcludeTags(scanExcludeTags)
	opts.MinUptimeDays = scanMinUptimeDays
	opts.Targets = scanTargets
	opts.Role = scanRole
	opts.ExternalID = scanExternalID
	opts.OrgScan = scanOrg
	opts.SkipSelf = scanSkipSelf

	awsAvailable := true
	result, err := awsprovider.Scan(ctx, opts)
	if err != nil {
		awsAvailable = false
		if scanSkipK8s {
			return fmt.Errorf("scan failed: %w", err)
		}
		// AWS scan failed but K8s scan may still work
		fmt.Fprintf(os.Stderr, "  warning: AWS scan failed: %v\n", err)
		result = &models.ScanResult{Timestamp: time.Now()}
	}

	// Kubernetes API scan
	if !scanSkipK8s {
		k8sOpts := k8sprovider.ScanOptions{
			Kubeconfig:   scanKubeconfig,
			Context:      scanKubeContext,
			PromURL:      scanPromURL,
			PromEndpoint: scanPromEndpoint,
		}
		k8sInstances, err := k8sprovider.Scan(ctx, k8sOpts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: Kubernetes scan failed: %v\n", err)
		} else if len(k8sInstances) > 0 {
			if !scanSkipMetrics {
				enrichK8sGPUMetrics(ctx, k8sInstances, k8sOpts, opts, awsAvailable)
			}
			analysis.AnalyzeAll(k8sInstances)
			result.Instances = append(result.Instances, k8sInstances...)
			result.Summary = awsprovider.BuildSummary(result.Instances)
		}
	}

	// Determine output writer
	w := os.Stdout
	if scanOutput != "" {
		f, err := os.Create(scanOutput)
		if err != nil {
			return fmt.Errorf("creating output file: %w", err)
		}
		defer f.Close()
		w = f
	}

	switch strings.ToLower(scanFormat) {
	case "json":
		return output.FormatJSON(w, result)
	case "markdown", "md":
		output.FormatMarkdown(w, result)
	case "slack":
		return output.FormatSlack(w, result)
	default:
		output.FormatTable(w, result)
	}

	return nil
}

func runDiff(cmd *cobra.Command, args []string) error {
	old, err := loadScanResult(args[0])
	if err != nil {
		return fmt.Errorf("loading old scan: %w", err)
	}
	new, err := loadScanResult(args[1])
	if err != nil {
		return fmt.Errorf("loading new scan: %w", err)
	}

	result := diff.Compare(old, new)

	switch strings.ToLower(diffFormat) {
	case "json":
		return output.FormatDiffJSON(os.Stdout, result)
	default:
		output.FormatDiffTable(os.Stdout, result)
	}

	return nil
}

func loadScanResult(path string) (*models.ScanResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result models.ScanResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &result, nil
}

// --- pricing command ---

var pricingGPU string

var pricingCmd = &cobra.Command{
	Use:   "pricing",
	Short: "Compare GPU instance pricing",
	RunE:  runPricing,
}

func init() {
	pricingCmd.Flags().StringVar(&pricingGPU, "gpu", "", "Filter by GPU model (e.g., H100, A100, A10G, L4, T4)")
}

func runPricing(cmd *cobra.Command, args []string) error {
	specs := pricing.AllEC2Specs()

	if pricingGPU != "" {
		filter := strings.ToUpper(pricingGPU)
		var filtered []pricing.GPUSpec
		for _, s := range specs {
			if strings.Contains(strings.ToUpper(s.GPUModel), filter) {
				filtered = append(filtered, s)
			}
		}
		specs = filtered
	}

	if len(specs) == 0 {
		fmt.Println("No matching GPU instance types found.")
		return nil
	}

	// Sort by price per GPU-hour ascending
	for i := 0; i < len(specs); i++ {
		for j := i + 1; j < len(specs); j++ {
			priceI := specs[i].OnDemandHourly / float64(specs[i].GPUCount)
			priceJ := specs[j].OnDemandHourly / float64(specs[j].GPUCount)
			if priceJ < priceI {
				specs[i], specs[j] = specs[j], specs[i]
			}
		}
	}

	fmt.Printf("\n  AWS GPU Instance Pricing (on-demand, us-east-1)\n\n")
	fmt.Printf("  %-20s %-12s %5s %8s %12s %12s\n",
		"Instance Type", "GPU Model", "GPUs", "VRAM", "$/hr", "$/GPU/hr")
	fmt.Printf("  %s %s %s %s %s %s\n",
		strings.Repeat("─", 20), strings.Repeat("─", 12),
		strings.Repeat("─", 5), strings.Repeat("─", 8),
		strings.Repeat("─", 12), strings.Repeat("─", 12))

	for _, s := range specs {
		perGPU := s.OnDemandHourly / float64(s.GPUCount)
		fmt.Printf("  %-20s %-12s %5d %5.0f GiB $%10.4f $%10.4f\n",
			s.InstanceType, s.GPUModel, s.GPUCount, s.TotalVRAMGiB,
			s.OnDemandHourly, perGPU)
	}
	fmt.Println()
	return nil
}

// --- iam-policy command ---

var iamPolicyCmd = &cobra.Command{
	Use:   "iam-policy",
	Short: "Print the minimal IAM policy required for gpuaudit",
	Run: func(cmd *cobra.Command, args []string) {
		policy := map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{
				{
					"Sid":    "GPUAuditEC2ReadOnly",
					"Effect": "Allow",
					"Action": []string{
						"ec2:DescribeInstances",
						"ec2:DescribeInstanceTypes",
						"ec2:DescribeRegions",
						"ec2:DescribeSpotPriceHistory",
					},
					"Resource": "*",
				},
				{
					"Sid":    "GPUAuditSageMakerReadOnly",
					"Effect": "Allow",
					"Action": []string{
						"sagemaker:ListEndpoints",
						"sagemaker:DescribeEndpoint",
						"sagemaker:DescribeEndpointConfig",
					},
					"Resource": "*",
				},
				{
					"Sid":    "GPUAuditEKSReadOnly",
					"Effect": "Allow",
					"Action": []string{
						"eks:ListClusters",
						"eks:ListNodegroups",
						"eks:DescribeNodegroup",
					},
					"Resource": "*",
				},
				{
					"Sid":    "GPUAuditCloudWatchReadOnly",
					"Effect": "Allow",
					"Action": []string{
						"cloudwatch:GetMetricData",
						"cloudwatch:GetMetricStatistics",
						"cloudwatch:ListMetrics",
					},
					"Resource": "*",
				},
				{
					"Sid":    "GPUAuditCostExplorerReadOnly",
					"Effect": "Allow",
					"Action": []string{
						"ce:GetCostAndUsage",
						"ce:GetReservationUtilization",
						"ce:GetSavingsPlansUtilization",
					},
					"Resource": "*",
				},
				{
					"Sid":    "GPUAuditPricingReadOnly",
					"Effect": "Allow",
					"Action": []string{
						"pricing:GetProducts",
					},
					"Resource": "*",
				},
				{
					"Sid":      "GPUAuditCrossAccount",
					"Effect":   "Allow",
					"Action":   "sts:AssumeRole",
					"Resource": "arn:aws:iam::*:role/gpuaudit-reader",
				},
				{
					"Sid":      "GPUAuditOrganizations",
					"Effect":   "Allow",
					"Action":   "organizations:ListAccounts",
					"Resource": "*",
				},
			},
		}
		fmt.Fprintln(os.Stdout, "// The last two statements (CrossAccount, Organizations) are only needed for --targets or --org scanning.")
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(policy)
	},
}

// --- version command ---

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print gpuaudit version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("gpuaudit %s\n", version)
	},
}

func parseExcludeTags(raw []string) map[string]string {
	tags := make(map[string]string, len(raw))
	for _, s := range raw {
		if k, v, ok := strings.Cut(s, "="); ok {
			tags[k] = v
		}
	}
	return tags
}

func enrichK8sGPUMetrics(ctx context.Context, instances []models.GPUInstance, k8sOpts k8sprovider.ScanOptions, awsOpts awsprovider.ScanOptions, awsAvailable bool) {
	// Source 1: CloudWatch Container Insights (skip if AWS creds unavailable)
	if awsAvailable && len(instances) > 0 && instances[0].ClusterName != "" {
		cfgOpts := []func(*awsconfig.LoadOptions) error{}
		if awsOpts.Profile != "" {
			cfgOpts = append(cfgOpts, awsconfig.WithSharedConfigProfile(awsOpts.Profile))
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
		if err == nil {
			region := instances[0].Region
			if region == "" {
				region = "us-east-1"
			}
			cfg.Region = region
			cwClient := cloudwatch.NewFromConfig(cfg)
			fmt.Fprintf(os.Stderr, "  Enriching K8s GPU metrics via CloudWatch Container Insights...\n")
			awsprovider.EnrichK8sGPUMetrics(ctx, cwClient, instances, instances[0].ClusterName, awsprovider.DefaultMetricWindow)
		}
	}

	// Source 2: DCGM exporter scrape
	remaining := 0
	for _, inst := range instances {
		if inst.Source == models.SourceK8sNode && inst.AvgGPUUtilization == nil {
			remaining++
		}
	}
	if remaining > 0 {
		client, _, err := k8sprovider.BuildClientPublic(k8sOpts.Kubeconfig, k8sOpts.Context)
		if err == nil {
			k8sprovider.EnrichDCGMMetrics(ctx, client, instances)
		}
	}

	// Source 3: Prometheus query
	remaining = 0
	for _, inst := range instances {
		if inst.Source == models.SourceK8sNode && inst.AvgGPUUtilization == nil {
			remaining++
		}
	}
	if remaining > 0 && (k8sOpts.PromURL != "" || k8sOpts.PromEndpoint != "") {
		var client k8sprovider.K8sClient
		if k8sOpts.PromEndpoint != "" {
			c, _, err := k8sprovider.BuildClientPublic(k8sOpts.Kubeconfig, k8sOpts.Context)
			if err == nil {
				client = c
			}
		}
		promOpts := k8sprovider.PrometheusOptions{
			URL:      k8sOpts.PromURL,
			Endpoint: k8sOpts.PromEndpoint,
		}
		k8sprovider.EnrichPrometheusMetrics(ctx, client, instances, promOpts)
	}
}
