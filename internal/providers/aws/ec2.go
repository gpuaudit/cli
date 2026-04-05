// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package aws implements GPU resource discovery for AWS.
package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/gpuaudit/gpuaudit/internal/models"
	"github.com/gpuaudit/gpuaudit/internal/pricing"
)

// EC2Client is the subset of the EC2 API we need.
type EC2Client interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// DiscoverEC2GPUInstances finds all running EC2 instances with GPUs in the given region.
func DiscoverEC2GPUInstances(ctx context.Context, client EC2Client, accountID, region string) ([]models.GPUInstance, error) {
	// Build filters for GPU instance families
	families := pricing.GPUFamilies()
	familyFilters := make([]string, 0, len(families))
	for _, f := range families {
		familyFilters = append(familyFilters, f+".*")
	}

	input := &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("instance-type"),
				Values: familyFilters,
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running", "stopped"},
			},
		},
	}

	var instances []models.GPUInstance

	paginator := ec2.NewDescribeInstancesPaginator(client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe instances in %s: %w", region, err)
		}

		for _, reservation := range page.Reservations {
			for _, inst := range reservation.Instances {
				gpuInst := ec2InstanceToGPU(inst, accountID, region)
				if gpuInst != nil {
					instances = append(instances, *gpuInst)
				}
			}
		}
	}

	return instances, nil
}

func ec2InstanceToGPU(inst ec2types.Instance, accountID, region string) *models.GPUInstance {
	instanceType := string(inst.InstanceType)

	spec := pricing.LookupEC2(instanceType)
	if spec == nil {
		return nil
	}

	now := time.Now()
	launchTime := aws.ToTime(inst.LaunchTime)
	uptimeHours := now.Sub(launchTime).Hours()

	tags := make(map[string]string)
	name := ""
	for _, tag := range inst.Tags {
		k := aws.ToString(tag.Key)
		v := aws.ToString(tag.Value)
		tags[k] = v
		if k == "Name" {
			name = v
		}
	}

	pricingModel := "on-demand"
	if inst.InstanceLifecycle == ec2types.InstanceLifecycleTypeSpot {
		pricingModel = "spot"
	}
	// TODO: detect RI/SP coverage via Cost Explorer

	return &models.GPUInstance{
		InstanceID:   aws.ToString(inst.InstanceId),
		Source:       models.SourceEC2,
		AccountID:    accountID,
		Region:       region,
		Name:         name,
		Tags:         tags,
		InstanceType: instanceType,
		GPUModel:     spec.GPUModel,
		GPUCount:     spec.GPUCount,
		GPUVRAMGiB:   spec.GPUVRAMGiB,
		TotalVRAMGiB: spec.TotalVRAMGiB,
		State:        strings.ToLower(string(inst.State.Name)),
		LaunchTime:   launchTime,
		UptimeHours:  uptimeHours,
		PricingModel: pricingModel,
		HourlyCost:   spec.OnDemandHourly,
		MonthlyCost:  spec.OnDemandHourly * 730,
	}
}
