package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	awsprovider "github.com/maksimov/gpuaudit/internal/providers/aws"
	"github.com/maksimov/gpuaudit/internal/output"
	"github.com/maksimov/gpuaudit/internal/pricing"
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
	scanSkipCosts     bool
	scanExcludeTags   []string
	scanMinIdleDays   int
)

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
	scanCmd.Flags().BoolVar(&scanSkipCosts, "skip-costs", false, "Skip Cost Explorer data enrichment")
	scanCmd.Flags().StringSliceVar(&scanExcludeTags, "exclude-tag", nil, "Exclude instances matching tag (key=value, repeatable)")
	scanCmd.Flags().IntVar(&scanMinIdleDays, "min-idle-days", 0, "Only report idle instances that have been idle for at least this many days")

	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(pricingCmd)
	rootCmd.AddCommand(iamPolicyCmd)
	rootCmd.AddCommand(versionCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	opts := awsprovider.DefaultScanOptions()
	opts.Profile = scanProfile
	opts.Regions = scanRegions
	opts.SkipMetrics = scanSkipMetrics
	opts.SkipSageMaker = scanSkipSageMaker
	opts.SkipCosts = scanSkipCosts
	opts.ExcludeTags = parseExcludeTags(scanExcludeTags)
	opts.MinIdleDays = scanMinIdleDays

	result, err := awsprovider.Scan(ctx, opts)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
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
			},
		}
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
