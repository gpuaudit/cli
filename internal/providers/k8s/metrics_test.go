// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
				dcgmPod("dcgm-exporter-abc", "gpu-operator", "ip-10-22-1-100.ec2.internal"),
			},
		},
		proxyData: map[string][]byte{
			"gpu-operator/dcgm-exporter-abc:9400/metrics": []byte(sampleDCGMMetrics),
		},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-abc123", K8sNodeName: "ip-10-22-1-100.ec2.internal", Source: models.SourceK8sNode, Name: "cluster/ip-10-22-1-100"},
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
				dcgmPod("dcgm-exporter-abc", "gpu-operator", "node1"),
			},
		},
		proxyData: map[string][]byte{
			"gpu-operator/dcgm-exporter-abc:9400/metrics": []byte(sampleDCGMMetrics),
		},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-abc123", K8sNodeName: "node1", Source: models.SourceK8sNode, AvgGPUUtilization: &gpuUtil},
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
				dcgmPod("dcgm-exporter-abc", "gpu-operator", "node1"),
			},
		},
		proxyErr: fmt.Errorf("connection refused"),
	}
	instances := []models.GPUInstance{
		{InstanceID: "node1", Source: models.SourceK8sNode},
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

func TestEnrichPrometheusMetrics_PopulatesFromDirectURL(t *testing.T) {
	promResponse := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{"metric": {"node": "i-node1"}, "value": [1700000000, "65.5"]},
				{"metric": {"node": "i-node2"}, "value": [1700000000, "30.0"]}
			]
		}
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		query := r.URL.Query().Get("query")
		if !strings.Contains(query, "DCGM_FI_DEV_GPU_UTIL") && !strings.Contains(query, "DCGM_FI_DEV_MEM_COPY_UTIL") {
			t.Errorf("unexpected query: %s", query)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(promResponse))
	}))
	defer server.Close()

	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode, Name: "cluster/i-node1"},
		{InstanceID: "i-node2", Source: models.SourceK8sNode, Name: "cluster/i-node2"},
	}
	opts := PrometheusOptions{URL: server.URL}

	enriched := EnrichPrometheusMetrics(context.Background(), nil, instances, opts)

	if enriched != 2 {
		t.Errorf("expected 2 enriched, got %d", enriched)
	}
	if instances[0].AvgGPUUtilization == nil || *instances[0].AvgGPUUtilization != 65.5 {
		t.Errorf("expected node1 GPU util 65.5, got %v", instances[0].AvgGPUUtilization)
	}
	if instances[1].AvgGPUUtilization == nil || *instances[1].AvgGPUUtilization != 30.0 {
		t.Errorf("expected node2 GPU util 30.0, got %v", instances[1].AvgGPUUtilization)
	}
}

func TestEnrichPrometheusMetrics_SkipsAlreadyEnriched(t *testing.T) {
	gpuUtil := 80.0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode, AvgGPUUtilization: &gpuUtil},
	}
	opts := PrometheusOptions{URL: server.URL}

	enriched := EnrichPrometheusMetrics(context.Background(), nil, instances, opts)

	if enriched != 0 {
		t.Errorf("expected 0 enriched, got %d", enriched)
	}
}

func TestEnrichPrometheusMetrics_NoOptions(t *testing.T) {
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode},
	}

	enriched := EnrichPrometheusMetrics(context.Background(), nil, instances, PrometheusOptions{})

	if enriched != 0 {
		t.Errorf("expected 0 enriched, got %d", enriched)
	}
}

func TestEnrichPrometheusMetrics_InClusterEndpoint(t *testing.T) {
	promResponse := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{"metric": {"node": "i-node1"}, "value": [1700000000, "50.0"]}
			]
		}
	}`
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode},
	}
	opts := PrometheusOptions{Endpoint: "monitoring/prometheus:9090"}

	// Use a custom client that returns promResponse for any ProxyGet to monitoring/prometheus
	customClient := &promMockClient{response: []byte(promResponse)}

	enriched := EnrichPrometheusMetrics(context.Background(), customClient, instances, opts)

	if enriched != 1 {
		t.Errorf("expected 1 enriched, got %d", enriched)
	}
	if instances[0].AvgGPUUtilization == nil || *instances[0].AvgGPUUtilization != 50.0 {
		t.Errorf("expected 50.0, got %v", instances[0].AvgGPUUtilization)
	}
}

// promMockClient is a specialized mock that always returns a fixed response for ProxyGet.
type promMockClient struct {
	mockK8sClient
	response []byte
}

func (m *promMockClient) ProxyGet(ctx context.Context, namespace, podName, port, path string) ([]byte, error) {
	return m.response, nil
}

func TestParsePrometheusEndpoint(t *testing.T) {
	tests := []struct {
		input     string
		namespace string
		service   string
		port      string
		wantErr   bool
	}{
		{"monitoring/prometheus:9090", "monitoring", "prometheus", "9090", false},
		{"kube-system/thanos-query:10902", "kube-system", "thanos-query", "10902", false},
		{"invalid", "", "", "", true},
		{"ns/svc", "", "", "", true},
	}
	for _, tt := range tests {
		ns, svc, port, err := parsePrometheusEndpoint(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parsePrometheusEndpoint(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			continue
		}
		if ns != tt.namespace || svc != tt.service || port != tt.port {
			t.Errorf("parsePrometheusEndpoint(%q) = (%q,%q,%q), want (%q,%q,%q)",
				tt.input, ns, svc, port, tt.namespace, tt.service, tt.port)
		}
	}
}
