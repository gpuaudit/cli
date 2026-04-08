// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gpuaudit/cli/internal/models"
)

type mockK8sClient struct {
	nodes *corev1.NodeList
	pods  *corev1.PodList
}

func (m *mockK8sClient) ListNodes(ctx context.Context, opts metav1.ListOptions) (*corev1.NodeList, error) {
	return m.nodes, nil
}

func (m *mockK8sClient) ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error) {
	return m.pods, nil
}

func gpuNode(name, instanceType string, gpuCount int, ready bool, created time.Time) corev1.Node {
	readyStatus := corev1.ConditionFalse
	if ready {
		readyStatus = corev1.ConditionTrue
	}
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			CreationTimestamp: metav1.NewTime(created),
			Labels: map[string]string{
				"node.kubernetes.io/instance-type": instanceType,
				"topology.kubernetes.io/region":    "us-east-1",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "aws:///us-east-1a/" + name,
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				gpuResourceName:      resource.MustParse(fmt.Sprintf("%d", gpuCount)),
				corev1.ResourceCPU:   resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: readyStatus},
			},
		},
	}
}

func gpuPod(name, namespace, nodeName string, gpuRequests int) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{
				{
					Name: "gpu-worker",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							gpuResourceName: resource.MustParse(fmt.Sprintf("%d", gpuRequests)),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}

func TestDiscoverGPUNodes_FindsGPUNodes(t *testing.T) {
	created := time.Now().Add(-48 * time.Hour)
	client := &mockK8sClient{
		nodes: &corev1.NodeList{
			Items: []corev1.Node{
				gpuNode("i-abc123", "g5.xlarge", 1, true, created),
			},
		},
		pods: &corev1.PodList{
			Items: []corev1.Pod{
				gpuPod("training-job", "ml", "i-abc123", 1),
			},
		},
	}

	instances, err := DiscoverGPUNodes(context.Background(), client, "ml-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}

	inst := instances[0]
	if inst.Source != models.SourceK8sNode {
		t.Errorf("expected source %s, got %s", models.SourceK8sNode, inst.Source)
	}
	if inst.InstanceType != "g5.xlarge" {
		t.Errorf("expected instance type g5.xlarge, got %s", inst.InstanceType)
	}
	if inst.ClusterName != "ml-cluster" {
		t.Errorf("expected cluster name ml-cluster, got %s", inst.ClusterName)
	}
	if inst.GPUAllocated != 1 {
		t.Errorf("expected 1 GPU allocated, got %d", inst.GPUAllocated)
	}
	if inst.GPUModel == "" {
		t.Error("expected GPU model to be populated from pricing DB")
	}
	if inst.InstanceID != "i-abc123" {
		t.Errorf("expected instance ID i-abc123, got %s", inst.InstanceID)
	}
}

func TestDiscoverGPUNodes_SkipsNonGPU(t *testing.T) {
	created := time.Now().Add(-24 * time.Hour)
	cpuNode := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "i-cpu123",
			CreationTimestamp: metav1.NewTime(created),
			Labels: map[string]string{
				"node.kubernetes.io/instance-type": "c5n.9xlarge",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:   resource.MustParse("36"),
				corev1.ResourceMemory: resource.MustParse("96Gi"),
			},
		},
	}

	client := &mockK8sClient{
		nodes: &corev1.NodeList{Items: []corev1.Node{cpuNode}},
		pods:  &corev1.PodList{Items: []corev1.Pod{}},
	}

	instances, err := DiscoverGPUNodes(context.Background(), client, "web-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(instances) != 0 {
		t.Fatalf("expected 0 instances for non-GPU node, got %d", len(instances))
	}
}

func TestDiscoverGPUNodes_IdleGPUNode(t *testing.T) {
	created := time.Now().Add(-72 * time.Hour)
	client := &mockK8sClient{
		nodes: &corev1.NodeList{
			Items: []corev1.Node{
				gpuNode("i-idle456", "g5.2xlarge", 1, true, created),
			},
		},
		pods: &corev1.PodList{Items: []corev1.Pod{}}, // no GPU pods
	}

	instances, err := DiscoverGPUNodes(context.Background(), client, "ml-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}

	inst := instances[0]
	if inst.GPUAllocated != 0 {
		t.Errorf("expected 0 GPUs allocated, got %d", inst.GPUAllocated)
	}
}

func TestDiscoverGPUNodes_PartialAllocation(t *testing.T) {
	created := time.Now().Add(-48 * time.Hour)
	client := &mockK8sClient{
		nodes: &corev1.NodeList{
			Items: []corev1.Node{
				gpuNode("i-multi789", "p4d.24xlarge", 8, true, created),
			},
		},
		pods: &corev1.PodList{
			Items: []corev1.Pod{
				gpuPod("job-1", "ml", "i-multi789", 2),
				gpuPod("job-2", "ml", "i-multi789", 1),
			},
		},
	}

	instances, err := DiscoverGPUNodes(context.Background(), client, "training-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}

	inst := instances[0]
	if inst.GPUAllocated != 3 {
		t.Errorf("expected 3 GPUs allocated, got %d", inst.GPUAllocated)
	}
}

func TestExtractEC2InstanceID(t *testing.T) {
	tests := []struct {
		providerID string
		want       string
	}{
		{"aws:///us-east-1a/i-0123456789abcdef0", "i-0123456789abcdef0"},
		{"aws:///us-west-2b/i-abc", "i-abc"},
		{"gce:///project/zone/instance", ""},
		{"", ""},
		{"aws:///", ""},
	}

	for _, tt := range tests {
		got := extractEC2InstanceID(tt.providerID)
		if got != tt.want {
			t.Errorf("extractEC2InstanceID(%q) = %q, want %q", tt.providerID, got, tt.want)
		}
	}
}
