// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"

	"github.com/maksimov/gpuaudit/internal/models"
	"github.com/maksimov/gpuaudit/internal/pricing"
)

// SageMakerClient is the subset of the SageMaker API we need.
type SageMakerClient interface {
	ListEndpoints(ctx context.Context, params *sagemaker.ListEndpointsInput, optFns ...func(*sagemaker.Options)) (*sagemaker.ListEndpointsOutput, error)
	DescribeEndpoint(ctx context.Context, params *sagemaker.DescribeEndpointInput, optFns ...func(*sagemaker.Options)) (*sagemaker.DescribeEndpointOutput, error)
	DescribeEndpointConfig(ctx context.Context, params *sagemaker.DescribeEndpointConfigInput, optFns ...func(*sagemaker.Options)) (*sagemaker.DescribeEndpointConfigOutput, error)
}

// DiscoverSageMakerEndpoints finds all active SageMaker endpoints with GPU instances.
func DiscoverSageMakerEndpoints(ctx context.Context, client SageMakerClient, accountID, region string) ([]models.GPUInstance, error) {
	var instances []models.GPUInstance

	input := &sagemaker.ListEndpointsInput{
		StatusEquals: smtypes.EndpointStatusInService,
	}

	paginator := sagemaker.NewListEndpointsPaginator(client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list sagemaker endpoints in %s: %w", region, err)
		}

		for _, ep := range page.Endpoints {
			gpuInstances, err := describeEndpointGPUs(ctx, client, ep, accountID, region)
			if err != nil {
				// Log but don't fail the entire scan for one endpoint
				fmt.Fprintf(os.Stderr,"  warning: could not describe endpoint %s: %v\n", aws.ToString(ep.EndpointName), err)
				continue
			}
			instances = append(instances, gpuInstances...)
		}
	}

	return instances, nil
}

func describeEndpointGPUs(ctx context.Context, client SageMakerClient, ep smtypes.EndpointSummary, accountID, region string) ([]models.GPUInstance, error) {
	endpointName := aws.ToString(ep.EndpointName)

	// Get endpoint details to find the config name
	epDetail, err := client.DescribeEndpoint(ctx, &sagemaker.DescribeEndpointInput{
		EndpointName: ep.EndpointName,
	})
	if err != nil {
		return nil, fmt.Errorf("describe endpoint: %w", err)
	}

	configName := aws.ToString(epDetail.EndpointConfigName)

	// Get endpoint config to find instance types
	config, err := client.DescribeEndpointConfig(ctx, &sagemaker.DescribeEndpointConfigInput{
		EndpointConfigName: &configName,
	})
	if err != nil {
		return nil, fmt.Errorf("describe endpoint config: %w", err)
	}

	var instances []models.GPUInstance
	now := time.Now()

	for _, variant := range config.ProductionVariants {
		instanceType := string(variant.InstanceType)
		spec := pricing.LookupSageMaker(instanceType)
		if spec == nil {
			continue // not a GPU instance type
		}

		instanceCount := int32(1)
		if variant.InitialInstanceCount != nil {
			instanceCount = *variant.InitialInstanceCount
		}

		creationTime := aws.ToTime(ep.CreationTime)
		uptimeHours := now.Sub(creationTime).Hours()

		for i := int32(0); i < instanceCount; i++ {
			instanceID := fmt.Sprintf("%s/%s", endpointName, aws.ToString(variant.VariantName))
			if instanceCount > 1 {
				instanceID = fmt.Sprintf("%s/%s/%d", endpointName, aws.ToString(variant.VariantName), i)
			}

			instances = append(instances, models.GPUInstance{
				InstanceID:   instanceID,
				Source:       models.SourceSageMakerEndpoint,
				AccountID:    accountID,
				Region:       region,
				Name:         endpointName,
				InstanceType: instanceType,
				GPUModel:     spec.GPUModel,
				GPUCount:     spec.GPUCount,
				GPUVRAMGiB:   spec.GPUVRAMGiB,
				TotalVRAMGiB: spec.TotalVRAMGiB,
				State:        "in-service",
				LaunchTime:   creationTime,
				UptimeHours:  uptimeHours,
				PricingModel: "on-demand",
				HourlyCost:   spec.OnDemandHourly,
				MonthlyCost:  spec.OnDemandHourly * 730,
			})
		}
	}

	return instances, nil
}
