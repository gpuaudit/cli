// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/gpuaudit/cli/internal/analysis"
	"github.com/gpuaudit/cli/internal/models"
)

// ScanOptions controls what gets scanned.
type ScanOptions struct {
	Profile       string
	Regions       []string
	MetricWindow  MetricWindow
	SkipMetrics   bool
	SkipSageMaker bool
	SkipEKS       bool
	SkipCosts     bool
	ExcludeTags   map[string]string
	MinUptimeDays int

	// Multi-target options
	Targets    []string
	Role       string
	ExternalID string
	OrgScan    bool
	SkipSelf   bool
}

// DefaultScanOptions returns sensible defaults.
func DefaultScanOptions() ScanOptions {
	return ScanOptions{
		MetricWindow: DefaultMetricWindow,
	}
}

// Scan performs a full GPU audit of the AWS account.
func Scan(ctx context.Context, opts ScanOptions) (*models.ScanResult, error) {
	start := time.Now()

	// Load AWS config
	cfgOpts := []func(*awsconfig.LoadOptions) error{}
	if opts.Profile != "" {
		cfgOpts = append(cfgOpts, awsconfig.WithSharedConfigProfile(opts.Profile))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	// Get account ID
	stsClient := sts.NewFromConfig(cfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("getting caller identity: %w", err)
	}
	accountID := aws.ToString(identity.Account)

	// Determine regions to scan
	regions := opts.Regions
	if len(regions) == 0 {
		regions, err = getGPURegions(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("listing regions: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr,"  Scanning %d regions for GPU instances...\n", len(regions))

	// Scan all regions concurrently
	type regionResult struct {
		region    string
		instances []models.GPUInstance
		err       error
	}

	results := make(chan regionResult, len(regions))
	var wg sync.WaitGroup

	for _, region := range regions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			instances, err := scanRegion(ctx, cfg, accountID, r, opts)
			results <- regionResult{region: r, instances: instances, err: err}
		}(region)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var allInstances []models.GPUInstance
	var scannedRegions []string

	for res := range results {
		if res.err != nil {
			fmt.Fprintf(os.Stderr,"  warning: error scanning %s: %v\n", res.region, res.err)
			continue
		}
		if len(res.instances) > 0 {
			allInstances = append(allInstances, res.instances...)
			scannedRegions = append(scannedRegions, res.region)
		}
	}

	// Filter by excluded tags
	if len(opts.ExcludeTags) > 0 {
		filtered := allInstances[:0]
		excluded := 0
		for _, inst := range allInstances {
			if matchesExcludeTags(inst.Tags, opts.ExcludeTags) {
				excluded++
				continue
			}
			filtered = append(filtered, inst)
		}
		allInstances = filtered
		if excluded > 0 {
			fmt.Fprintf(os.Stderr, "  Excluded %d instance(s) by tag filter.\n", excluded)
		}
	}

	// Enrich with Cost Explorer data (account-level, not per-region)
	if !opts.SkipCosts && len(allInstances) > 0 {
		ceClient := costexplorer.NewFromConfig(cfg)
		if err := EnrichCostData(ctx, ceClient, allInstances); err != nil {
			fmt.Fprintf(os.Stderr,"  warning: could not enrich cost data: %v\n", err)
		}
	}

	// Run analysis
	analysis.AnalyzeAll(allInstances)

	// Suppress all signals on instances below the minimum uptime threshold
	if opts.MinUptimeDays > 0 {
		minHours := float64(opts.MinUptimeDays) * 24
		for i := range allInstances {
			inst := &allInstances[i]
			if inst.UptimeHours >= minHours {
				continue
			}
			inst.WasteSignals = nil
			inst.Recommendations = nil
			inst.EstimatedSavings = 0
		}
	}

	// Build summary
	summary := BuildSummary(allInstances)

	return &models.ScanResult{
		Timestamp:    start,
		AccountID:    accountID,
		Regions:      scannedRegions,
		ScanDuration: time.Since(start).Round(time.Millisecond).String(),
		Instances:    allInstances,
		Summary:      summary,
	}, nil
}

func scanRegion(ctx context.Context, cfg aws.Config, accountID, region string, opts ScanOptions) ([]models.GPUInstance, error) {
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region

	ec2Client := ec2.NewFromConfig(regionalCfg)
	cwClient := cloudwatch.NewFromConfig(regionalCfg)

	var allInstances []models.GPUInstance

	// Discover EC2 GPU instances
	ec2Instances, err := DiscoverEC2GPUInstances(ctx, ec2Client, accountID, region)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not scan EC2 in %s: %v\n", region, err)
	} else {
		if !opts.SkipMetrics && len(ec2Instances) > 0 {
			if err := EnrichEC2Metrics(ctx, cwClient, ec2Instances, opts.MetricWindow); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not enrich EC2 metrics in %s: %v\n", region, err)
			}
		}
		allInstances = append(allInstances, ec2Instances...)
	}

	// Discover SageMaker endpoints
	if !opts.SkipSageMaker {
		smClient := sagemaker.NewFromConfig(regionalCfg)
		smInstances, err := DiscoverSageMakerEndpoints(ctx, smClient, accountID, region)
		if err != nil {
			fmt.Fprintf(os.Stderr,"  warning: could not scan SageMaker in %s: %v\n", region, err)
		} else {
			if !opts.SkipMetrics && len(smInstances) > 0 {
				if err := EnrichSageMakerMetrics(ctx, cwClient, smInstances, opts.MetricWindow); err != nil {
					fmt.Fprintf(os.Stderr,"  warning: could not enrich SageMaker metrics in %s: %v\n", region, err)
				}
			}
			allInstances = append(allInstances, smInstances...)
		}
	}

	// Discover EKS GPU node groups
	if !opts.SkipEKS {
		eksClient := eks.NewFromConfig(regionalCfg)
		eksInstances, err := DiscoverEKSGPUNodeGroups(ctx, eksClient, accountID, region)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not scan EKS in %s: %v\n", region, err)
		} else {
			allInstances = append(allInstances, eksInstances...)
		}
	}

	return allInstances, nil
}

func getGPURegions(ctx context.Context, cfg aws.Config) ([]string, error) {
	// Scan the most common regions where GPUs are available
	return []string{
		"us-east-1", "us-east-2", "us-west-1", "us-west-2",
		"eu-west-1", "eu-west-2", "eu-central-1",
		"ap-southeast-1", "ap-northeast-1",
	}, nil
}

func matchesExcludeTags(instanceTags map[string]string, excludes map[string]string) bool {
	for k, v := range excludes {
		if instanceTags[k] == v {
			return true
		}
	}
	return false
}
