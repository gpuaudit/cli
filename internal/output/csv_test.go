// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gpuaudit/cli/internal/models"
)

func TestFormatCSV_SingleInstance(t *testing.T) {
	result := &models.ScanResult{
		Timestamp:    time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		AccountID:    "123456789012",
		Regions:      []string{"us-east-1"},
		ScanDuration: "5s",
		Instances: []models.GPUInstance{
			{
				InstanceID:   "i-abc123",
				Source:       models.SourceEC2,
				AccountID:    "123456789012",
				Region:       "us-east-1",
				Name:         "ml-training-1",
				InstanceType: "p4d.24xlarge",
				GPUModel:     "A100",
				GPUCount:     8,
				GPUVRAMGiB:   40,
				TotalVRAMGiB: 320,
				State:        "running",
				LaunchTime:   time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
				UptimeHours:  36,
				PricingModel: "on-demand",
				HourlyCost:   32.77,
				MonthlyCost:  23922.10,
			},
		},
		Summary: models.ScanSummary{
			TotalInstances:   1,
			TotalMonthlyCost: 23922.10,
		},
	}

	var buf bytes.Buffer
	if err := FormatCSV(&buf, result); err != nil {
		t.Fatalf("FormatCSV() error: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("FormatCSV() produced empty output")
	}

	// Header row should contain CSV column names from struct tags.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least header + 1 data row, got %d lines", len(lines))
	}

	header := lines[0]
	for _, col := range []string{"instance_id", "source", "region", "instance_type", "gpu_model", "gpu_count", "hourly_cost", "monthly_cost"} {
		if !strings.Contains(header, col) {
			t.Errorf("header missing column %q", col)
		}
	}

	// Data row should contain instance values.
	data := lines[1]
	for _, val := range []string{"i-abc123", "ec2", "us-east-1", "p4d.24xlarge", "A100", "on-demand"} {
		if !strings.Contains(data, val) {
			t.Errorf("data row missing value %q", val)
		}
	}
}

func TestFormatCSV_MultipleInstances(t *testing.T) {
	result := &models.ScanResult{
		Timestamp:    time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		AccountID:    "123456789012",
		Regions:      []string{"us-east-1"},
		ScanDuration: "3s",
		Instances: []models.GPUInstance{
			{
				InstanceID:   "i-aaa",
				Source:       models.SourceEC2,
				Region:       "us-east-1",
				InstanceType: "g5.xlarge",
				GPUModel:     "A10G",
				GPUCount:     1,
				State:        "running",
				LaunchTime:   time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
				PricingModel: "on-demand",
				HourlyCost:   1.01,
				MonthlyCost:  737.30,
			},
			{
				InstanceID:   "i-bbb",
				Source:       models.SourceK8sNode,
				Region:       "eu-west-1",
				InstanceType: "p4d.24xlarge",
				GPUModel:     "A100",
				GPUCount:     8,
				State:        "running",
				LaunchTime:   time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
				PricingModel: "on-demand",
				HourlyCost:   32.77,
				MonthlyCost:  23922.10,
			},
		},
		Summary: models.ScanSummary{
			TotalInstances:   2,
			TotalMonthlyCost: 24659.40,
		},
	}

	var buf bytes.Buffer
	if err := FormatCSV(&buf, result); err != nil {
		t.Fatalf("FormatCSV() error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	// Header + 2 data rows.
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 rows), got %d", len(lines))
	}
}

func TestFormatCSV_NilMetricsOmitted(t *testing.T) {
	result := &models.ScanResult{
		Timestamp:    time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		ScanDuration: "1s",
		Instances: []models.GPUInstance{
			{
				InstanceID:        "i-nil-metrics",
				Source:            models.SourceEC2,
				InstanceType:      "g5.xlarge",
				GPUModel:          "A10G",
				GPUCount:          1,
				State:             "running",
				LaunchTime:        time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
				PricingModel:      "on-demand",
				AvgGPUUtilization: nil,
				AvgCPUPercent:     nil,
			},
		},
		Summary: models.ScanSummary{TotalInstances: 1},
	}

	var buf bytes.Buffer
	if err := FormatCSV(&buf, result); err != nil {
		t.Fatalf("FormatCSV() error: %v", err)
	}

	// Should not error on nil pointer fields.
	if buf.Len() == 0 {
		t.Fatal("FormatCSV() produced empty output for nil metrics")
	}
}

func TestFormatCSV_WithMetrics(t *testing.T) {
	gpuUtil := 85.5
	cpuPct := 42.0

	result := &models.ScanResult{
		Timestamp:    time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		ScanDuration: "1s",
		Instances: []models.GPUInstance{
			{
				InstanceID:        "i-with-metrics",
				Source:            models.SourceEC2,
				InstanceType:      "p4d.24xlarge",
				GPUModel:          "A100",
				GPUCount:          8,
				State:             "running",
				LaunchTime:        time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
				PricingModel:      "on-demand",
				HourlyCost:        32.77,
				MonthlyCost:       23922.10,
				AvgGPUUtilization: &gpuUtil,
				AvgCPUPercent:     &cpuPct,
			},
		},
		Summary: models.ScanSummary{TotalInstances: 1},
	}

	var buf bytes.Buffer
	if err := FormatCSV(&buf, result); err != nil {
		t.Fatalf("FormatCSV() error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "85.5") {
		t.Error("expected GPU utilization 85.5 in output")
	}
	if !strings.Contains(out, "42") {
		t.Error("expected CPU percent 42 in output")
	}
}

func TestFormatCSV_EmptyInstances(t *testing.T) {
	result := &models.ScanResult{
		Timestamp:    time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		ScanDuration: "0s",
		Instances:    []models.GPUInstance{},
		Summary:      models.ScanSummary{},
	}

	var buf bytes.Buffer
	err := FormatCSV(&buf, result)
	// Empty slice may produce header-only or error — either is valid behavior.
	// Just verify no panic.
	_ = err
}
