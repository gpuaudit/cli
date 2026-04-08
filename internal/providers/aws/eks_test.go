// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/gpuaudit/cli/internal/models"
)

type mockEKSClient struct {
	clusters   []string
	nodegroups map[string][]string                       // cluster -> nodegroup names
	details    map[string]map[string]*ekstypes.Nodegroup // cluster -> ng -> detail
}

func (m *mockEKSClient) ListClusters(ctx context.Context, params *eks.ListClustersInput, optFns ...func(*eks.Options)) (*eks.ListClustersOutput, error) {
	return &eks.ListClustersOutput{Clusters: m.clusters}, nil
}

func (m *mockEKSClient) ListNodegroups(ctx context.Context, params *eks.ListNodegroupsInput, optFns ...func(*eks.Options)) (*eks.ListNodegroupsOutput, error) {
	cluster := aws.ToString(params.ClusterName)
	return &eks.ListNodegroupsOutput{Nodegroups: m.nodegroups[cluster]}, nil
}

func (m *mockEKSClient) DescribeNodegroup(ctx context.Context, params *eks.DescribeNodegroupInput, optFns ...func(*eks.Options)) (*eks.DescribeNodegroupOutput, error) {
	cluster := aws.ToString(params.ClusterName)
	ng := aws.ToString(params.NodegroupName)
	return &eks.DescribeNodegroupOutput{Nodegroup: m.details[cluster][ng]}, nil
}

func TestDiscoverEKSGPUNodeGroups_FindsGPUNodes(t *testing.T) {
	created := time.Now().Add(-48 * time.Hour)
	client := &mockEKSClient{
		clusters:   []string{"ml-cluster"},
		nodegroups: map[string][]string{"ml-cluster": {"gpu-workers"}},
		details: map[string]map[string]*ekstypes.Nodegroup{
			"ml-cluster": {
				"gpu-workers": {
					NodegroupName: aws.String("gpu-workers"),
					ClusterName:   aws.String("ml-cluster"),
					Status:        ekstypes.NodegroupStatusActive,
					InstanceTypes: []string{"g5.xlarge"},
					ScalingConfig: &ekstypes.NodegroupScalingConfig{
						DesiredSize: aws.Int32(3),
					},
					CreatedAt: &created,
					Tags:      map[string]string{"team": "ml"},
				},
			},
		},
	}

	instances, err := DiscoverEKSGPUNodeGroups(context.Background(), client, "123456789012", "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(instances) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(instances))
	}

	inst := instances[0]
	if inst.Source != models.SourceEKS {
		t.Errorf("expected source %s, got %s", models.SourceEKS, inst.Source)
	}
	if inst.InstanceType != "g5.xlarge" {
		t.Errorf("expected instance type g5.xlarge, got %s", inst.InstanceType)
	}
	if inst.GPUModel == "" {
		t.Error("expected GPU model to be populated")
	}
	if inst.Name != "ml-cluster/gpu-workers" {
		t.Errorf("expected name ml-cluster/gpu-workers, got %s", inst.Name)
	}
	if inst.Tags["team"] != "ml" {
		t.Error("expected tags to be populated")
	}
}

func TestDiscoverEKSGPUNodeGroups_SkipsNonGPU(t *testing.T) {
	created := time.Now().Add(-24 * time.Hour)
	client := &mockEKSClient{
		clusters:   []string{"web-cluster"},
		nodegroups: map[string][]string{"web-cluster": {"cpu-workers"}},
		details: map[string]map[string]*ekstypes.Nodegroup{
			"web-cluster": {
				"cpu-workers": {
					NodegroupName: aws.String("cpu-workers"),
					ClusterName:   aws.String("web-cluster"),
					Status:        ekstypes.NodegroupStatusActive,
					InstanceTypes: []string{"m5.xlarge"},
					ScalingConfig: &ekstypes.NodegroupScalingConfig{
						DesiredSize: aws.Int32(5),
					},
					CreatedAt: &created,
				},
			},
		},
	}

	instances, err := DiscoverEKSGPUNodeGroups(context.Background(), client, "123456789012", "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(instances) != 0 {
		t.Fatalf("expected 0 instances for non-GPU node group, got %d", len(instances))
	}
}

func TestDiscoverEKSGPUNodeGroups_SkipsInactiveNodeGroup(t *testing.T) {
	created := time.Now().Add(-24 * time.Hour)
	client := &mockEKSClient{
		clusters:   []string{"cluster"},
		nodegroups: map[string][]string{"cluster": {"gpu-ng"}},
		details: map[string]map[string]*ekstypes.Nodegroup{
			"cluster": {
				"gpu-ng": {
					NodegroupName: aws.String("gpu-ng"),
					ClusterName:   aws.String("cluster"),
					Status:        ekstypes.NodegroupStatusDeleting,
					InstanceTypes: []string{"g5.xlarge"},
					ScalingConfig: &ekstypes.NodegroupScalingConfig{
						DesiredSize: aws.Int32(2),
					},
					CreatedAt: &created,
				},
			},
		},
	}

	instances, err := DiscoverEKSGPUNodeGroups(context.Background(), client, "123456789012", "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(instances) != 0 {
		t.Fatalf("expected 0 instances for deleting node group, got %d", len(instances))
	}
}

func TestDiscoverEKSGPUNodeGroups_NoClusters(t *testing.T) {
	client := &mockEKSClient{
		clusters: []string{},
	}

	instances, err := DiscoverEKSGPUNodeGroups(context.Background(), client, "123456789012", "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(instances) != 0 {
		t.Fatalf("expected 0 instances, got %d", len(instances))
	}
}
