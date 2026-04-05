// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/gpuaudit/cli/internal/models"
)

type mockCEClient struct {
	results map[string][]cetypes.ResultByTime // keyed by service name
}

func (m *mockCEClient) GetCostAndUsage(ctx context.Context, params *costexplorer.GetCostAndUsageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error) {
	// Extract the service name from the filter to return the right data
	if params.Filter != nil && len(params.Filter.And) >= 1 {
		if dims := params.Filter.And[0].Dimensions; dims != nil && len(dims.Values) > 0 {
			service := dims.Values[0]
			if results, ok := m.results[service]; ok {
				return &costexplorer.GetCostAndUsageOutput{ResultsByTime: results}, nil
			}
		}
	}
	return &costexplorer.GetCostAndUsageOutput{}, nil
}

func TestEnrichCostData_AppliesMTDCost(t *testing.T) {
	client := &mockCEClient{
		results: map[string][]cetypes.ResultByTime{
			"Amazon Elastic Compute Cloud - Compute": {
				{
					Groups: []cetypes.Group{
						{
							Keys: []string{"g5.xlarge"},
							Metrics: map[string]cetypes.MetricValue{
								"UnblendedCost": {Amount: aws.String("200.50")},
							},
						},
					},
				},
			},
		},
	}

	instances := []models.GPUInstance{
		{
			InstanceID:   "i-abc",
			Source:       models.SourceEC2,
			InstanceType: "g5.xlarge",
		},
		{
			InstanceID:   "i-def",
			Source:       models.SourceEC2,
			InstanceType: "g5.xlarge",
		},
	}

	err := EnrichCostData(context.Background(), client, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Two instances of same type, cost should be split evenly
	for _, inst := range instances {
		if inst.MTDCost == nil {
			t.Fatalf("expected MTDCost for %s", inst.InstanceID)
		}
		expected := 100.25
		if *inst.MTDCost != expected {
			t.Errorf("expected $%.2f MTD cost for %s, got $%.2f", expected, inst.InstanceID, *inst.MTDCost)
		}
	}
}

func TestEnrichCostData_EmptyInstances(t *testing.T) {
	err := EnrichCostData(context.Background(), &mockCEClient{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnrichCostData_NoCostData(t *testing.T) {
	client := &mockCEClient{results: map[string][]cetypes.ResultByTime{}}

	instances := []models.GPUInstance{
		{InstanceType: "p5.48xlarge"},
	}

	err := EnrichCostData(context.Background(), client, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instances[0].MTDCost != nil {
		t.Error("expected nil MTDCost when no cost data returned")
	}
}
