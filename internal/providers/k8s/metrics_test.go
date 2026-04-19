// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gpuaudit/cli/internal/models"
)

func dcgmPod(name, namespace, nodeName string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "dcgm-exporter",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}

const sampleDCGMMetrics = `# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization.
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-abc",device="nvidia0",modelName="NVIDIA A10G",Hostname="node1"} 42.0
DCGM_FI_DEV_GPU_UTIL{gpu="1",UUID="GPU-def",device="nvidia1",modelName="NVIDIA A10G",Hostname="node1"} 38.0
# HELP DCGM_FI_DEV_MEM_COPY_UTIL GPU memory utilization.
# TYPE DCGM_FI_DEV_MEM_COPY_UTIL gauge
DCGM_FI_DEV_MEM_COPY_UTIL{gpu="0",UUID="GPU-abc",device="nvidia0",modelName="NVIDIA A10G",Hostname="node1"} 55.0
DCGM_FI_DEV_MEM_COPY_UTIL{gpu="1",UUID="GPU-def",device="nvidia1",modelName="NVIDIA A10G",Hostname="node1"} 60.0
`

func TestEnrichDCGMMetrics_PopulatesUtilization(t *testing.T) {
	client := &mockK8sClient{
		nodes: &corev1.NodeList{},
		pods: &corev1.PodList{
			Items: []corev1.Pod{
				dcgmPod("dcgm-exporter-abc", "gpu-operator", "i-node1"),
			},
		},
		proxyData: map[string][]byte{
			"gpu-operator/dcgm-exporter-abc:9400/metrics": []byte(sampleDCGMMetrics),
		},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode, Name: "cluster/i-node1"},
	}

	enriched := EnrichDCGMMetrics(context.Background(), client, instances)

	if instances[0].AvgGPUUtilization == nil {
		t.Fatal("expected GPU utilization to be populated")
	}
	if *instances[0].AvgGPUUtilization != 40.0 {
		t.Errorf("expected avg GPU util 40.0 (average of 42 and 38), got %f", *instances[0].AvgGPUUtilization)
	}
	if instances[0].AvgGPUMemUtilization == nil {
		t.Fatal("expected GPU memory utilization to be populated")
	}
	if *instances[0].AvgGPUMemUtilization != 57.5 {
		t.Errorf("expected avg GPU mem util 57.5 (average of 55 and 60), got %f", *instances[0].AvgGPUMemUtilization)
	}
	if enriched != 1 {
		t.Errorf("expected 1 enriched node, got %d", enriched)
	}
}

func TestEnrichDCGMMetrics_SkipsAlreadyEnriched(t *testing.T) {
	gpuUtil := 75.0
	client := &mockK8sClient{
		nodes: &corev1.NodeList{},
		pods: &corev1.PodList{
			Items: []corev1.Pod{
				dcgmPod("dcgm-exporter-abc", "gpu-operator", "i-node1"),
			},
		},
		proxyData: map[string][]byte{
			"gpu-operator/dcgm-exporter-abc:9400/metrics": []byte(sampleDCGMMetrics),
		},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode, AvgGPUUtilization: &gpuUtil},
	}

	enriched := EnrichDCGMMetrics(context.Background(), client, instances)

	if *instances[0].AvgGPUUtilization != 75.0 {
		t.Error("should not overwrite existing utilization")
	}
	if enriched != 0 {
		t.Errorf("expected 0 enriched nodes, got %d", enriched)
	}
}

func TestEnrichDCGMMetrics_NoDCGMPods(t *testing.T) {
	client := &mockK8sClient{
		nodes: &corev1.NodeList{},
		pods:  &corev1.PodList{Items: []corev1.Pod{}},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode},
	}

	enriched := EnrichDCGMMetrics(context.Background(), client, instances)

	if instances[0].AvgGPUUtilization != nil {
		t.Error("expected nil when no DCGM pods")
	}
	if enriched != 0 {
		t.Errorf("expected 0 enriched, got %d", enriched)
	}
}

func TestEnrichDCGMMetrics_HandlesScrapeError(t *testing.T) {
	client := &mockK8sClient{
		nodes: &corev1.NodeList{},
		pods: &corev1.PodList{
			Items: []corev1.Pod{
				dcgmPod("dcgm-exporter-abc", "gpu-operator", "i-node1"),
			},
		},
		proxyErr: fmt.Errorf("connection refused"),
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode},
	}

	enriched := EnrichDCGMMetrics(context.Background(), client, instances)

	if instances[0].AvgGPUUtilization != nil {
		t.Error("expected nil after scrape error")
	}
	if enriched != 0 {
		t.Errorf("expected 0 enriched, got %d", enriched)
	}
}

func TestParseDCGMMetrics(t *testing.T) {
	gpuUtil, memUtil := parseDCGMMetrics([]byte(sampleDCGMMetrics))

	if gpuUtil == nil {
		t.Fatal("expected gpu util")
	}
	if *gpuUtil != 40.0 {
		t.Errorf("expected 40.0, got %f", *gpuUtil)
	}
	if memUtil == nil {
		t.Fatal("expected mem util")
	}
	if *memUtil != 57.5 {
		t.Errorf("expected 57.5, got %f", *memUtil)
	}
}

func TestParseDCGMMetrics_EmptyInput(t *testing.T) {
	gpuUtil, memUtil := parseDCGMMetrics([]byte(""))
	if gpuUtil != nil || memUtil != nil {
		t.Error("expected nil for empty input")
	}
}
