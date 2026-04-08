// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/gpuaudit/cli/internal/models"
	"github.com/gpuaudit/cli/internal/pricing"
)

// EKSClient is the subset of the EKS API we need.
type EKSClient interface {
	ListClusters(ctx context.Context, params *eks.ListClustersInput, optFns ...func(*eks.Options)) (*eks.ListClustersOutput, error)
	ListNodegroups(ctx context.Context, params *eks.ListNodegroupsInput, optFns ...func(*eks.Options)) (*eks.ListNodegroupsOutput, error)
	DescribeNodegroup(ctx context.Context, params *eks.DescribeNodegroupInput, optFns ...func(*eks.Options)) (*eks.DescribeNodegroupOutput, error)
}

// DiscoverEKSGPUNodeGroups finds EKS managed node groups running GPU instance types.
func DiscoverEKSGPUNodeGroups(ctx context.Context, client EKSClient, accountID, region string) ([]models.GPUInstance, error) {
	clusters, err := listAllClusters(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("list EKS clusters in %s: %w", region, err)
	}

	var instances []models.GPUInstance

	for _, clusterName := range clusters {
		nodegroups, err := listAllNodegroups(ctx, client, clusterName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not list node groups for cluster %s: %v\n", clusterName, err)
			continue
		}

		for _, ngName := range nodegroups {
			gpuInstances, err := describeNodegroupGPUs(ctx, client, clusterName, ngName, accountID, region)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not describe node group %s/%s: %v\n", clusterName, ngName, err)
				continue
			}
			instances = append(instances, gpuInstances...)
		}
	}

	return instances, nil
}

func listAllClusters(ctx context.Context, client EKSClient) ([]string, error) {
	var clusters []string
	var nextToken *string

	for {
		out, err := client.ListClusters(ctx, &eks.ListClustersInput{
			NextToken: nextToken,
		})
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, out.Clusters...)
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	return clusters, nil
}

func listAllNodegroups(ctx context.Context, client EKSClient, clusterName string) ([]string, error) {
	var nodegroups []string
	var nextToken *string

	for {
		out, err := client.ListNodegroups(ctx, &eks.ListNodegroupsInput{
			ClusterName: &clusterName,
			NextToken:   nextToken,
		})
		if err != nil {
			return nil, err
		}
		nodegroups = append(nodegroups, out.Nodegroups...)
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	return nodegroups, nil
}

func describeNodegroupGPUs(ctx context.Context, client EKSClient, clusterName, ngName, accountID, region string) ([]models.GPUInstance, error) {
	out, err := client.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   &clusterName,
		NodegroupName: &ngName,
	})
	if err != nil {
		return nil, err
	}

	ng := out.Nodegroup
	if ng == nil {
		return nil, nil
	}

	// Only care about ACTIVE node groups
	if ng.Status != ekstypes.NodegroupStatusActive {
		return nil, nil
	}

	// Find GPU instance types in this node group
	var gpuSpecs []pricing.GPUSpec
	var gpuInstanceTypes []string
	for _, it := range ng.InstanceTypes {
		spec := pricing.LookupEC2(it)
		if spec != nil {
			gpuSpecs = append(gpuSpecs, *spec)
			gpuInstanceTypes = append(gpuInstanceTypes, it)
		}
	}

	if len(gpuSpecs) == 0 {
		return nil, nil
	}

	// Use the first GPU instance type as representative (node groups typically use one type)
	spec := gpuSpecs[0]
	instanceType := gpuInstanceTypes[0]

	now := time.Now()
	desiredSize := int32(0)
	if ng.ScalingConfig != nil && ng.ScalingConfig.DesiredSize != nil {
		desiredSize = int32(*ng.ScalingConfig.DesiredSize)
	}

	createdAt := aws.ToTime(ng.CreatedAt)
	uptimeHours := now.Sub(createdAt).Hours()

	tags := make(map[string]string)
	for k, v := range ng.Tags {
		tags[k] = v
	}

	var instances []models.GPUInstance

	// Create one GPUInstance per node in the desired count
	for i := int32(0); i < desiredSize; i++ {
		instanceID := fmt.Sprintf("%s/%s", clusterName, ngName)
		name := fmt.Sprintf("%s/%s", clusterName, ngName)
		if desiredSize > 1 {
			instanceID = fmt.Sprintf("%s/%s/%d", clusterName, ngName, i)
		}

		instances = append(instances, models.GPUInstance{
			InstanceID:   instanceID,
			Source:       models.SourceEKS,
			AccountID:    accountID,
			Region:       region,
			Name:         name,
			Tags:         tags,
			InstanceType: instanceType,
			GPUModel:     spec.GPUModel,
			GPUCount:     spec.GPUCount,
			GPUVRAMGiB:   spec.GPUVRAMGiB,
			TotalVRAMGiB: spec.TotalVRAMGiB,
			State:        "active",
			LaunchTime:   createdAt,
			UptimeHours:  uptimeHours,
			PricingModel: "on-demand",
			HourlyCost:   spec.OnDemandHourly,
			MonthlyCost:  spec.OnDemandHourly * 730,
		})
	}

	return instances, nil
}
