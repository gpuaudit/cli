// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/gpuaudit/cli/internal/models"
)

type mockCloudWatchClient struct {
	output *cloudwatch.GetMetricDataOutput
	err    error
}

func (m *mockCloudWatchClient) GetMetricData(ctx context.Context, params *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.output, nil
}

func TestEnrichK8sGPUMetrics_PopulatesUtilization(t *testing.T) {
	client := &mockCloudWatchClient{
		output: &cloudwatch.GetMetricDataOutput{
			MetricDataResults: []cwtypes.MetricDataResult{
				{Id: aws.String("gpu_util_i_abc123"), Values: []float64{45.0, 50.0, 55.0}},
				{Id: aws.String("gpu_mem_i_abc123"), Values: []float64{30.0, 35.0, 40.0}},
			},
		},
	}
	instances := []models.GPUInstance{
		{
			InstanceID: "i-abc123",
			Source:     models.SourceK8sNode,
		},
	}

	EnrichK8sGPUMetrics(context.Background(), client, instances, "ml-cluster", DefaultMetricWindow)

	if instances[0].AvgGPUUtilization == nil {
		t.Fatal("expected GPU utilization to be populated")
	}
	if *instances[0].AvgGPUUtilization != 50.0 {
		t.Errorf("expected avg GPU util 50.0, got %f", *instances[0].AvgGPUUtilization)
	}
	if instances[0].AvgGPUMemUtilization == nil {
		t.Fatal("expected GPU memory utilization to be populated")
	}
	if *instances[0].AvgGPUMemUtilization != 35.0 {
		t.Errorf("expected avg GPU mem util 35.0, got %f", *instances[0].AvgGPUMemUtilization)
	}
}

func TestEnrichK8sGPUMetrics_SkipsNonK8sNodes(t *testing.T) {
	client := &mockCloudWatchClient{
		output: &cloudwatch.GetMetricDataOutput{},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-ec2", Source: models.SourceEC2},
	}

	EnrichK8sGPUMetrics(context.Background(), client, instances, "cluster", DefaultMetricWindow)

	if instances[0].AvgGPUUtilization != nil {
		t.Error("expected nil GPU util for non-K8s instance")
	}
}

func TestEnrichK8sGPUMetrics_SkipsNodesWithoutInstanceID(t *testing.T) {
	client := &mockCloudWatchClient{
		output: &cloudwatch.GetMetricDataOutput{},
	}
	instances := []models.GPUInstance{
		{InstanceID: "node-hostname", Source: models.SourceK8sNode},
	}

	EnrichK8sGPUMetrics(context.Background(), client, instances, "cluster", DefaultMetricWindow)

	if instances[0].AvgGPUUtilization != nil {
		t.Error("expected nil GPU util for node without EC2 instance ID")
	}
}

func TestEnrichK8sGPUMetrics_SkipsAlreadyEnriched(t *testing.T) {
	gpuUtil := 75.0
	client := &mockCloudWatchClient{
		output: &cloudwatch.GetMetricDataOutput{},
	}
	instances := []models.GPUInstance{
		{
			InstanceID:        "i-abc123",
			Source:            models.SourceK8sNode,
			AvgGPUUtilization: &gpuUtil,
		},
	}

	EnrichK8sGPUMetrics(context.Background(), client, instances, "cluster", DefaultMetricWindow)

	if *instances[0].AvgGPUUtilization != 75.0 {
		t.Errorf("expected existing value 75.0 to be preserved, got %f", *instances[0].AvgGPUUtilization)
	}
}

func TestEnrichK8sGPUMetrics_HandlesAPIError(t *testing.T) {
	client := &mockCloudWatchClient{
		err: fmt.Errorf("access denied"),
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-abc123", Source: models.SourceK8sNode},
	}

	EnrichK8sGPUMetrics(context.Background(), client, instances, "cluster", DefaultMetricWindow)

	if instances[0].AvgGPUUtilization != nil {
		t.Error("expected nil GPU util after API error")
	}
}
