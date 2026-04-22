// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gpuaudit/cli/internal/models"
)

func TestExtractIPFromDNS(t *testing.T) {
	tests := []struct {
		dns    string
		wantIP string
	}{
		{"ip-10-22-249-234.ec2.internal", "10.22.249.234"},
		{"ip-172-31-0-5.us-west-2.compute.internal", "172.31.0.5"},
		{"custom-hostname.ec2.internal", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractIPFromDNS(tt.dns)
		if got != tt.wantIP {
			t.Errorf("extractIPFromDNS(%q) = %q, want %q", tt.dns, got, tt.wantIP)
		}
	}
}

func TestEnrichEC2PrometheusGPUMetrics_MatchesByHostname(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "GPU_UTIL") {
			fmt.Fprintf(w, `{
				"status": "success",
				"data": {"resultType": "vector", "result": [
					{"metric": {"Hostname": "ip-10-0-1-100"}, "value": [1700000000, "72.5"]}
				]}
			}`)
		} else {
			fmt.Fprintf(w, `{
				"status": "success",
				"data": {"resultType": "vector", "result": [
					{"metric": {"Hostname": "ip-10-0-1-100"}, "value": [1700000000, "45.0"]}
				]}
			}`)
		}
	}))
	defer srv.Close()

	instances := []models.GPUInstance{
		{
			InstanceID:     "i-abc123",
			Source:         models.SourceEC2,
			State:          "running",
			PrivateDnsName: "ip-10-0-1-100.ec2.internal",
		},
		{
			InstanceID:     "i-def456",
			Source:         models.SourceEC2,
			State:          "running",
			PrivateDnsName: "ip-10-0-1-200.ec2.internal",
		},
	}

	enriched := EnrichEC2PrometheusGPUMetrics(context.Background(), srv.URL, instances)

	if enriched != 1 {
		t.Fatalf("expected 1 enriched, got %d", enriched)
	}
	if instances[0].AvgGPUUtilization == nil || *instances[0].AvgGPUUtilization != 72.5 {
		t.Errorf("expected GPU util 72.5, got %v", instances[0].AvgGPUUtilization)
	}
	if instances[0].AvgGPUMemUtilization == nil || *instances[0].AvgGPUMemUtilization != 45.0 {
		t.Errorf("expected GPU mem util 45.0, got %v", instances[0].AvgGPUMemUtilization)
	}
	if instances[1].AvgGPUUtilization != nil {
		t.Error("expected no GPU util for unmatched instance")
	}
}

func TestEnrichEC2PrometheusGPUMetrics_FallsBackToInstanceLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "Hostname") {
			// No results by hostname
			fmt.Fprintf(w, `{"status": "success", "data": {"resultType": "vector", "result": []}}`)
		} else if strings.Contains(query, "instance") && strings.Contains(query, "GPU_UTIL") {
			fmt.Fprintf(w, `{
				"status": "success",
				"data": {"resultType": "vector", "result": [
					{"metric": {"instance": "10.0.1.100:9400"}, "value": [1700000000, "88.0"]}
				]}
			}`)
		} else {
			fmt.Fprintf(w, `{
				"status": "success",
				"data": {"resultType": "vector", "result": [
					{"metric": {"instance": "10.0.1.100:9400"}, "value": [1700000000, "60.0"]}
				]}
			}`)
		}
	}))
	defer srv.Close()

	instances := []models.GPUInstance{
		{
			InstanceID:     "i-abc123",
			Source:         models.SourceEC2,
			State:          "running",
			PrivateDnsName: "ip-10-0-1-100.ec2.internal",
		},
	}

	enriched := EnrichEC2PrometheusGPUMetrics(context.Background(), srv.URL, instances)

	if enriched != 1 {
		t.Fatalf("expected 1 enriched, got %d", enriched)
	}
	if instances[0].AvgGPUUtilization == nil || *instances[0].AvgGPUUtilization != 88.0 {
		t.Errorf("expected GPU util 88.0, got %v", instances[0].AvgGPUUtilization)
	}
}

func TestEnrichEC2PrometheusGPUMetrics_SkipsAlreadyEnriched(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not query Prometheus when all instances already have metrics")
		fmt.Fprintf(w, `{"status": "success", "data": {"resultType": "vector", "result": []}}`)
	}))
	defer srv.Close()

	gpuUtil := 50.0
	instances := []models.GPUInstance{
		{
			InstanceID:        "i-abc123",
			Source:            models.SourceEC2,
			State:             "running",
			PrivateDnsName:    "ip-10-0-1-100.ec2.internal",
			AvgGPUUtilization: &gpuUtil,
		},
	}

	enriched := EnrichEC2PrometheusGPUMetrics(context.Background(), srv.URL, instances)
	if enriched != 0 {
		t.Errorf("expected 0 enriched, got %d", enriched)
	}
}

func TestEnrichEC2PrometheusGPUMetrics_SkipsNonEC2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not query Prometheus for non-EC2 instances")
		fmt.Fprintf(w, `{"status": "success", "data": {"resultType": "vector", "result": []}}`)
	}))
	defer srv.Close()

	instances := []models.GPUInstance{
		{
			InstanceID:     "node-1",
			Source:         models.SourceK8sNode,
			State:          "ready",
			PrivateDnsName: "ip-10-0-1-100.ec2.internal",
		},
	}

	enriched := EnrichEC2PrometheusGPUMetrics(context.Background(), srv.URL, instances)
	if enriched != 0 {
		t.Errorf("expected 0 enriched, got %d", enriched)
	}
}
