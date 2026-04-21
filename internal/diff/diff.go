// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package diff compares two scan results and reports what changed.
package diff

import (
	"fmt"

	"github.com/gpuaudit/cli/internal/models"
)

// DiffResult holds the comparison between two scan results.
type DiffResult struct {
	OldTimestamp   string               `json:"old_timestamp"`
	NewTimestamp   string               `json:"new_timestamp"`
	Added          []models.GPUInstance `json:"added,omitempty"`
	Removed        []models.GPUInstance `json:"removed,omitempty"`
	Changed        []InstanceDiff       `json:"changed,omitempty"`
	UnchangedCount int                  `json:"unchanged_count"`
	CostSummary    CostDelta            `json:"cost_summary"`
}

// InstanceDiff describes what changed for a single instance between scans.
type InstanceDiff struct {
	InstanceID string             `json:"instance_id"`
	Old        models.GPUInstance `json:"old"`
	New        models.GPUInstance `json:"new"`
	CostDelta  float64            `json:"cost_delta"`
	Changes    []string           `json:"changes"`
}

// CostDelta summarizes the financial impact of changes between scans.
type CostDelta struct {
	OldTotalMonthlyCost float64 `json:"old_total_monthly_cost"`
	NewTotalMonthlyCost float64 `json:"new_total_monthly_cost"`
	CostChange          float64 `json:"cost_change"`
	OldTotalWaste       float64 `json:"old_total_waste"`
	NewTotalWaste       float64 `json:"new_total_waste"`
	WasteChange         float64 `json:"waste_change"`
	AddedCost           float64 `json:"added_cost"`
	RemovedSavings      float64 `json:"removed_savings"`
}

// Compare computes the diff between two scan results, matching instances by ID.
func Compare(old, new *models.ScanResult) *DiffResult {
	oldMap := make(map[string]models.GPUInstance, len(old.Instances))
	for _, inst := range old.Instances {
		oldMap[inst.InstanceID] = inst
	}

	newMap := make(map[string]models.GPUInstance, len(new.Instances))
	for _, inst := range new.Instances {
		newMap[inst.InstanceID] = inst
	}

	result := &DiffResult{
		OldTimestamp: old.Timestamp.Format("2006-01-02 15:04 UTC"),
		NewTimestamp: new.Timestamp.Format("2006-01-02 15:04 UTC"),
	}

	// Find removed and changed
	for id, oldInst := range oldMap {
		newInst, exists := newMap[id]
		if !exists {
			result.Removed = append(result.Removed, oldInst)
			continue
		}
		changes := diffInstance(oldInst, newInst)
		if len(changes) > 0 {
			result.Changed = append(result.Changed, InstanceDiff{
				InstanceID: id,
				Old:        oldInst,
				New:        newInst,
				CostDelta:  newInst.MonthlyCost - oldInst.MonthlyCost,
				Changes:    changes,
			})
		} else {
			result.UnchangedCount++
		}
	}

	// Find added
	for id, newInst := range newMap {
		if _, exists := oldMap[id]; !exists {
			result.Added = append(result.Added, newInst)
		}
	}

	// Cost summary
	result.CostSummary = computeCostDelta(old, new, result)

	return result
}

func diffInstance(old, new models.GPUInstance) []string {
	var changes []string

	if old.InstanceType != new.InstanceType {
		changes = append(changes, fmt.Sprintf("Instance type: %s → %s", old.InstanceType, new.InstanceType))
	}
	if old.PricingModel != new.PricingModel {
		changes = append(changes, fmt.Sprintf("Pricing: %s → %s", old.PricingModel, new.PricingModel))
	}
	if old.MonthlyCost != new.MonthlyCost {
		delta := new.MonthlyCost - old.MonthlyCost
		changes = append(changes, fmt.Sprintf("Cost: $%.0f → $%.0f (%s/mo)", old.MonthlyCost, new.MonthlyCost, fmtDelta(delta)))
	}
	if old.State != new.State {
		changes = append(changes, fmt.Sprintf("State: %s → %s", old.State, new.State))
	}
	if old.GPUAllocated != new.GPUAllocated {
		changes = append(changes, fmt.Sprintf("GPU allocated: %d → %d", old.GPUAllocated, new.GPUAllocated))
	}
	if maxSeverityStr(old.WasteSignals) != maxSeverityStr(new.WasteSignals) {
		oldSev := maxSeverityStr(old.WasteSignals)
		newSev := maxSeverityStr(new.WasteSignals)
		if oldSev == "" {
			oldSev = "(none)"
		}
		if newSev == "" {
			newSev = "(none)"
		}
		changes = append(changes, fmt.Sprintf("Severity: %s → %s", oldSev, newSev))
	}

	return changes
}

func maxSeverityStr(signals []models.WasteSignal) string {
	max := models.Severity("")
	for _, s := range signals {
		if s.Severity == models.SeverityCritical {
			return string(models.SeverityCritical)
		}
		if s.Severity == models.SeverityWarning {
			max = models.SeverityWarning
		}
		if s.Severity == models.SeverityInfo && max == "" {
			max = models.SeverityInfo
		}
	}
	return string(max)
}

func fmtDelta(v float64) string {
	if v >= 0 {
		return fmt.Sprintf("+$%.0f", v)
	}
	return fmt.Sprintf("-$%.0f", -v)
}

func computeCostDelta(old, new *models.ScanResult, diff *DiffResult) CostDelta {
	cd := CostDelta{
		OldTotalMonthlyCost: old.Summary.TotalMonthlyCost,
		NewTotalMonthlyCost: new.Summary.TotalMonthlyCost,
		CostChange:          new.Summary.TotalMonthlyCost - old.Summary.TotalMonthlyCost,
		OldTotalWaste:       old.Summary.TotalEstimatedWaste,
		NewTotalWaste:       new.Summary.TotalEstimatedWaste,
		WasteChange:         new.Summary.TotalEstimatedWaste - old.Summary.TotalEstimatedWaste,
	}

	for _, inst := range diff.Added {
		cd.AddedCost += inst.MonthlyCost
	}
	for _, inst := range diff.Removed {
		cd.RemovedSavings += inst.MonthlyCost
	}

	return cd
}
