// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/gpuaudit/cli/internal/models"
)

type mockSpotPriceClient struct {
	prices []ec2types.SpotPrice
	err    error
}

func (m *mockSpotPriceClient) DescribeSpotPriceHistory(ctx context.Context, params *ec2.DescribeSpotPriceHistoryInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSpotPriceHistoryOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &ec2.DescribeSpotPriceHistoryOutput{
		SpotPriceHistory: m.prices,
	}, nil
}

func TestEnrichSpotPrices_PopulatesSpotCost(t *testing.T) {
	client := &mockSpotPriceClient{
		prices: []ec2types.SpotPrice{
			{
				InstanceType: ec2types.InstanceTypeG5Xlarge,
				SpotPrice:    aws.String("0.556"),
				Timestamp:    aws.Time(time.Now()),
			},
			{
				InstanceType: ec2types.InstanceTypeG5Xlarge,
				SpotPrice:    aws.String("0.500"),
				Timestamp:    aws.Time(time.Now().Add(-1 * time.Hour)),
			},
		},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-1", InstanceType: "g5.xlarge", Source: models.SourceEC2},
		{InstanceID: "i-2", InstanceType: "g5.2xlarge", Source: models.SourceEC2},
	}

	EnrichSpotPrices(context.Background(), client, instances)

	if instances[0].SpotHourlyCost == nil {
		t.Fatal("expected spot price for g5.xlarge")
	}
	if *instances[0].SpotHourlyCost != 0.556 {
		t.Errorf("expected 0.556, got %f", *instances[0].SpotHourlyCost)
	}
	if instances[1].SpotHourlyCost != nil {
		t.Error("expected nil spot price for g5.2xlarge (not in API response)")
	}
}

func TestEnrichSpotPrices_SkipsNonEC2(t *testing.T) {
	client := &mockSpotPriceClient{
		prices: []ec2types.SpotPrice{
			{
				InstanceType: ec2types.InstanceTypeG5Xlarge,
				SpotPrice:    aws.String("0.556"),
				Timestamp:    aws.Time(time.Now()),
			},
		},
	}
	instances := []models.GPUInstance{
		{InstanceID: "ep-1", InstanceType: "ml.g5.xlarge", Source: models.SourceSageMakerEndpoint},
	}

	EnrichSpotPrices(context.Background(), client, instances)

	if instances[0].SpotHourlyCost != nil {
		t.Error("expected nil spot price for SageMaker instance")
	}
}

func TestEnrichSpotPrices_HandlesAPIError(t *testing.T) {
	client := &mockSpotPriceClient{
		err: fmt.Errorf("access denied"),
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-1", InstanceType: "g5.xlarge", Source: models.SourceEC2},
	}

	EnrichSpotPrices(context.Background(), client, instances)

	if instances[0].SpotHourlyCost != nil {
		t.Error("expected nil spot price after API error")
	}
}

func TestEnrichSpotPrices_EmptyInstances(t *testing.T) {
	client := &mockSpotPriceClient{}
	EnrichSpotPrices(context.Background(), client, nil)
	EnrichSpotPrices(context.Background(), client, []models.GPUInstance{})
}

func TestEnrichSpotPrices_CorrectsCostForSpotInstances(t *testing.T) {
	client := &mockSpotPriceClient{
		prices: []ec2types.SpotPrice{
			{
				InstanceType: ec2types.InstanceTypeG5Xlarge,
				SpotPrice:    aws.String("0.556"),
				Timestamp:    aws.Time(time.Now()),
			},
		},
	}
	instances := []models.GPUInstance{
		{
			InstanceID:   "i-spot",
			InstanceType: "g5.xlarge",
			Source:       models.SourceEC2,
			PricingModel: "spot",
			HourlyCost:   1.006, // on-demand price (wrong for spot)
			MonthlyCost:  1.006 * 730,
		},
		{
			InstanceID:   "i-ondemand",
			InstanceType: "g5.xlarge",
			Source:       models.SourceEC2,
			PricingModel: "on-demand",
			HourlyCost:   1.006,
			MonthlyCost:  1.006 * 730,
		},
	}

	EnrichSpotPrices(context.Background(), client, instances)

	// Spot instance should have corrected cost
	if instances[0].HourlyCost != 0.556 {
		t.Errorf("spot instance hourly cost: expected 0.556, got %f", instances[0].HourlyCost)
	}
	expectedMonthlyCost := 0.556 * 730
	const epsilon = 0.0001
	if instances[0].MonthlyCost < expectedMonthlyCost-epsilon || instances[0].MonthlyCost > expectedMonthlyCost+epsilon {
		t.Errorf("spot instance monthly cost: expected %f, got %f", expectedMonthlyCost, instances[0].MonthlyCost)
	}

	// On-demand instance should keep original cost
	if instances[1].HourlyCost != 1.006 {
		t.Errorf("on-demand instance hourly cost should be unchanged, got %f", instances[1].HourlyCost)
	}
}
