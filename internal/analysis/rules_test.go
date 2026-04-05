// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"testing"
	"time"

	"github.com/maksimov/gpuaudit/internal/models"
)

func ptr[T any](v T) *T { return &v }

func TestRuleIdle_CriticalWhenLowCPUAndNetwork(t *testing.T) {
	inst := models.GPUInstance{
		InstanceID:         "i-test123",
		Source:             models.SourceEC2,
		State:              "running",
		InstanceType:       "g5.xlarge",
		GPUModel:           "A10G",
		GPUCount:           1,
		UptimeHours:        72,
		MonthlyCost:        734,
		AvgCPUPercent:      ptr(0.5),
		AvgNetworkInBytes:  ptr(500.0),
		AvgNetworkOutBytes: ptr(200.0),
	}

	ruleIdle(&inst)

	if len(inst.WasteSignals) != 1 {
		t.Fatalf("expected 1 waste signal, got %d", len(inst.WasteSignals))
	}
	if inst.WasteSignals[0].Severity != models.SeverityCritical {
		t.Errorf("expected critical severity, got %s", inst.WasteSignals[0].Severity)
	}
	if inst.WasteSignals[0].Type != "idle" {
		t.Errorf("expected idle type, got %s", inst.WasteSignals[0].Type)
	}
	if len(inst.Recommendations) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(inst.Recommendations))
	}
	if inst.Recommendations[0].Action != models.ActionTerminate {
		t.Errorf("expected terminate action, got %s", inst.Recommendations[0].Action)
	}
}

func TestRuleIdle_SkipsHighCPU(t *testing.T) {
	inst := models.GPUInstance{
		State:         "running",
		UptimeHours:   72,
		AvgCPUPercent: ptr(45.0),
	}

	ruleIdle(&inst)

	if len(inst.WasteSignals) != 0 {
		t.Errorf("expected no signals for high CPU, got %d", len(inst.WasteSignals))
	}
}

func TestRuleIdle_SkipsRecentInstances(t *testing.T) {
	inst := models.GPUInstance{
		State:         "running",
		UptimeHours:   2, // less than 24h
		AvgCPUPercent: ptr(0.1),
	}

	ruleIdle(&inst)

	if len(inst.WasteSignals) != 0 {
		t.Errorf("expected no signals for recent instance, got %d", len(inst.WasteSignals))
	}
}

func TestRuleOversizedGPU_FlagsMultiGPUWithLowCPU(t *testing.T) {
	inst := models.GPUInstance{
		InstanceType:       "g5.12xlarge",
		GPUModel:           "A10G",
		GPUCount:           4,
		MonthlyCost:        4140,
		AvgCPUPercent:      ptr(5.0),
		AvgNetworkOutBytes: ptr(1000.0), // low network
	}

	ruleOversizedGPU(&inst)

	if len(inst.WasteSignals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(inst.WasteSignals))
	}
	if inst.WasteSignals[0].Type != "oversized_gpu" {
		t.Errorf("expected oversized_gpu, got %s", inst.WasteSignals[0].Type)
	}
	if inst.Recommendations[0].Action != models.ActionDownsize {
		t.Errorf("expected downsize action, got %s", inst.Recommendations[0].Action)
	}
	if inst.Recommendations[0].MonthlySavings <= 0 {
		t.Errorf("expected positive savings, got %f", inst.Recommendations[0].MonthlySavings)
	}
}

func TestRuleOversizedGPU_SkipsSingleGPU(t *testing.T) {
	inst := models.GPUInstance{
		InstanceType:  "g5.xlarge",
		GPUCount:      1,
		AvgCPUPercent: ptr(2.0),
	}

	ruleOversizedGPU(&inst)

	if len(inst.WasteSignals) != 0 {
		t.Errorf("expected no signals for single-GPU instance")
	}
}

func TestRulePricingMismatch_FlagsLongRunningOnDemand(t *testing.T) {
	inst := models.GPUInstance{
		PricingModel: "on-demand",
		UptimeHours:  1440, // 60 days
		MonthlyCost:  5000,
	}

	rulePricingMismatch(&inst)

	if len(inst.WasteSignals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(inst.WasteSignals))
	}
	if inst.WasteSignals[0].Type != "pricing_mismatch" {
		t.Errorf("expected pricing_mismatch, got %s", inst.WasteSignals[0].Type)
	}
	if inst.Recommendations[0].MonthlySavings <= 0 {
		t.Error("expected positive savings")
	}
}

func TestRulePricingMismatch_SkipsSpot(t *testing.T) {
	inst := models.GPUInstance{
		PricingModel: "spot",
		UptimeHours:  1440,
	}

	rulePricingMismatch(&inst)

	if len(inst.WasteSignals) != 0 {
		t.Errorf("expected no signals for spot instance")
	}
}

func TestRuleStale_FlagsOldDevInstances(t *testing.T) {
	inst := models.GPUInstance{
		UptimeHours: 2500, // > 90 days
		MonthlyCost: 1000,
		Name:        "dev-ml-experiment",
		Tags:        map[string]string{"Environment": "development"},
	}

	ruleStale(&inst)

	if len(inst.WasteSignals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(inst.WasteSignals))
	}
	if inst.WasteSignals[0].Type != "stale" {
		t.Errorf("expected stale, got %s", inst.WasteSignals[0].Type)
	}
}

func TestRuleStale_SkipsProd(t *testing.T) {
	inst := models.GPUInstance{
		UptimeHours: 2500,
		Name:        "asr-server-prod",
		Tags:        map[string]string{"Environment": "production"},
	}

	ruleStale(&inst)

	if len(inst.WasteSignals) != 0 {
		t.Errorf("expected no signals for production instance")
	}
}

func TestRuleSageMakerLowUtil(t *testing.T) {
	inst := models.GPUInstance{
		Source:            models.SourceSageMakerEndpoint,
		AvgGPUUtilization: ptr(3.5),
		MonthlyCost:       9000,
	}

	ruleSageMakerLowUtil(&inst)

	if len(inst.WasteSignals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(inst.WasteSignals))
	}
	if inst.WasteSignals[0].Severity != models.SeverityCritical {
		t.Errorf("expected critical, got %s", inst.WasteSignals[0].Severity)
	}
}

func TestRuleSageMakerOversized(t *testing.T) {
	inst := models.GPUInstance{
		Source:               models.SourceSageMakerEndpoint,
		InstanceType:         "ml.g6e.48xlarge",
		GPUModel:             "L40S",
		GPUCount:             8,
		GPUVRAMGiB:           48,
		TotalVRAMGiB:         384,
		AvgGPUMemUtilization: ptr(5.0), // using ~19 GiB of 384 GiB
		MonthlyCost:          26800,
	}

	ruleSageMakerOversized(&inst)

	if len(inst.WasteSignals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(inst.WasteSignals))
	}
	if inst.Recommendations[0].MonthlySavings <= 0 {
		t.Error("expected positive savings")
	}
}

func TestAnalyzeAll_ComputesSavings(t *testing.T) {
	instances := []models.GPUInstance{
		{
			InstanceID:         "i-idle",
			Source:             models.SourceEC2,
			State:              "running",
			InstanceType:       "g5.xlarge",
			GPUModel:           "A10G",
			GPUCount:           1,
			UptimeHours:        100,
			MonthlyCost:        734,
			AvgCPUPercent:      ptr(0.3),
			AvgNetworkInBytes:  ptr(100.0),
			AvgNetworkOutBytes: ptr(100.0),
			LaunchTime:         time.Now().Add(-100 * time.Hour),
		},
		{
			InstanceID:   "i-healthy",
			Source:       models.SourceEC2,
			State:        "running",
			InstanceType: "g5.xlarge",
			GPUModel:     "A10G",
			GPUCount:     1,
			UptimeHours:  10,
			MonthlyCost:  734,
			LaunchTime:   time.Now().Add(-10 * time.Hour),
		},
	}

	AnalyzeAll(instances)

	// Idle instance should have savings
	if instances[0].EstimatedSavings == 0 {
		t.Error("expected savings for idle instance")
	}

	// Healthy instance should have no signals
	if len(instances[1].WasteSignals) != 0 {
		t.Errorf("expected no signals for healthy instance, got %d", len(instances[1].WasteSignals))
	}
}
