// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package diff

import (
	"testing"
	"time"

	"github.com/gpuaudit/cli/internal/models"
)

func scanResult(instances ...models.GPUInstance) *models.ScanResult {
	return &models.ScanResult{
		Timestamp: time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
		Instances: instances,
		Summary: models.ScanSummary{
			TotalInstances:      len(instances),
			TotalMonthlyCost:    sumMonthlyCost(instances),
			TotalEstimatedWaste: sumWaste(instances),
		},
	}
}

func sumMonthlyCost(instances []models.GPUInstance) float64 {
	var total float64
	for _, inst := range instances {
		total += inst.MonthlyCost
	}
	return total
}

func sumWaste(instances []models.GPUInstance) float64 {
	var total float64
	for _, inst := range instances {
		total += inst.EstimatedSavings
	}
	return total
}

func inst(id string, monthlyCost float64) models.GPUInstance {
	return models.GPUInstance{
		InstanceID:   id,
		InstanceType: "g6e.16xlarge",
		GPUModel:     "L40S",
		GPUCount:     1,
		MonthlyCost:  monthlyCost,
		HourlyCost:   monthlyCost / 730,
		State:        "ready",
		Source:       models.SourceK8sNode,
		PricingModel: "on-demand",
	}
}

func TestCompare_AddedInstances(t *testing.T) {
	old := scanResult(inst("i-aaa", 6750))
	new := scanResult(inst("i-aaa", 6750), inst("i-bbb", 3000))

	result := Compare(old, new)

	if len(result.Added) != 1 {
		t.Fatalf("expected 1 added, got %d", len(result.Added))
	}
	if result.Added[0].InstanceID != "i-bbb" {
		t.Errorf("expected added instance i-bbb, got %s", result.Added[0].InstanceID)
	}
	if result.CostSummary.AddedCost != 3000 {
		t.Errorf("expected added cost 3000, got %.0f", result.CostSummary.AddedCost)
	}
}

func TestCompare_RemovedInstances(t *testing.T) {
	old := scanResult(inst("i-aaa", 6750), inst("i-bbb", 3000))
	new := scanResult(inst("i-aaa", 6750))

	result := Compare(old, new)

	if len(result.Removed) != 1 {
		t.Fatalf("expected 1 removed, got %d", len(result.Removed))
	}
	if result.Removed[0].InstanceID != "i-bbb" {
		t.Errorf("expected removed instance i-bbb, got %s", result.Removed[0].InstanceID)
	}
	if result.CostSummary.RemovedSavings != 3000 {
		t.Errorf("expected removed savings 3000, got %.0f", result.CostSummary.RemovedSavings)
	}
}

func TestCompare_CostChange(t *testing.T) {
	old := scanResult(inst("i-aaa", 6750))
	new := scanResult(inst("i-aaa", 4200))

	result := Compare(old, new)

	if len(result.Changed) != 1 {
		t.Fatalf("expected 1 changed, got %d", len(result.Changed))
	}
	if result.Changed[0].CostDelta != -2550 {
		t.Errorf("expected cost delta -2550, got %.0f", result.Changed[0].CostDelta)
	}
	found := false
	for _, c := range result.Changed[0].Changes {
		if c == "Cost: $6750 → $4200 (-$2550/mo)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected cost change string, got %v", result.Changed[0].Changes)
	}
}

func TestCompare_AllFieldChanges(t *testing.T) {
	oldInst := inst("i-aaa", 6750)
	oldInst.InstanceType = "g6e.16xlarge"
	oldInst.PricingModel = "on-demand"
	oldInst.State = "ready"
	oldInst.GPUAllocated = 0
	oldInst.WasteSignals = []models.WasteSignal{{Severity: models.SeverityCritical}}

	newInst := inst("i-aaa", 4200)
	newInst.InstanceType = "g6e.12xlarge"
	newInst.PricingModel = "reserved"
	newInst.State = "not-ready"
	newInst.GPUAllocated = 2
	newInst.WasteSignals = nil

	old := scanResult(oldInst)
	new := scanResult(newInst)

	result := Compare(old, new)

	if len(result.Changed) != 1 {
		t.Fatalf("expected 1 changed, got %d", len(result.Changed))
	}

	changes := result.Changed[0].Changes
	expected := []string{
		"Instance type: g6e.16xlarge → g6e.12xlarge",
		"Pricing: on-demand → reserved",
		"Cost: $6750 → $4200 (-$2550/mo)",
		"State: ready → not-ready",
		"GPU allocated: 0 → 2",
		"Severity: critical → (none)",
	}
	if len(changes) != len(expected) {
		t.Fatalf("expected %d changes, got %d: %v", len(expected), len(changes), changes)
	}
	for i, exp := range expected {
		if changes[i] != exp {
			t.Errorf("change[%d]: expected %q, got %q", i, exp, changes[i])
		}
	}
}

func TestCompare_UnchangedInstances(t *testing.T) {
	old := scanResult(inst("i-aaa", 6750), inst("i-bbb", 3000))
	new := scanResult(inst("i-aaa", 6750), inst("i-bbb", 3000))

	result := Compare(old, new)

	if len(result.Added) != 0 {
		t.Errorf("expected 0 added, got %d", len(result.Added))
	}
	if len(result.Removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(result.Removed))
	}
	if len(result.Changed) != 0 {
		t.Errorf("expected 0 changed, got %d", len(result.Changed))
	}
	if result.UnchangedCount != 2 {
		t.Errorf("expected 2 unchanged, got %d", result.UnchangedCount)
	}
}

func TestCompare_CostSummary(t *testing.T) {
	oldA := inst("i-aaa", 6750)
	oldA.EstimatedSavings = 6750
	oldB := inst("i-bbb", 3000)

	newA := inst("i-aaa", 6750)
	newA.EstimatedSavings = 6750
	newC := inst("i-ccc", 2000)

	old := scanResult(oldA, oldB)
	new := scanResult(newA, newC)

	result := Compare(old, new)

	cs := result.CostSummary
	if cs.OldTotalMonthlyCost != 9750 {
		t.Errorf("expected old total 9750, got %.0f", cs.OldTotalMonthlyCost)
	}
	if cs.NewTotalMonthlyCost != 8750 {
		t.Errorf("expected new total 8750, got %.0f", cs.NewTotalMonthlyCost)
	}
	if cs.CostChange != -1000 {
		t.Errorf("expected cost change -1000, got %.0f", cs.CostChange)
	}
	if cs.RemovedSavings != 3000 {
		t.Errorf("expected removed savings 3000, got %.0f", cs.RemovedSavings)
	}
	if cs.AddedCost != 2000 {
		t.Errorf("expected added cost 2000, got %.0f", cs.AddedCost)
	}
}

func TestCompare_EmptyScans(t *testing.T) {
	old := scanResult()
	new := scanResult()

	result := Compare(old, new)

	if len(result.Added) != 0 || len(result.Removed) != 0 || len(result.Changed) != 0 {
		t.Errorf("expected no changes for empty scans")
	}
	if result.UnchangedCount != 0 {
		t.Errorf("expected 0 unchanged, got %d", result.UnchangedCount)
	}
}
