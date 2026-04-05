// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package output provides formatting for scan results.
package output

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/gpuaudit/cli/internal/models"
)

// FormatTable writes a human-readable table report to the writer.
func FormatTable(w io.Writer, result *models.ScanResult) {
	s := result.Summary

	// Header
	fmt.Fprintf(w, "\n  gpuaudit — GPU Cost Audit for AWS\n")
	fmt.Fprintf(w, "  Account: %s | Regions: %s | Duration: %s\n\n",
		result.AccountID,
		strings.Join(result.Regions, ", "),
		result.ScanDuration,
	)

	// Summary box
	fmt.Fprintf(w, "  ┌──────────────────────────────────────────────────────────┐\n")
	fmt.Fprintf(w, "  │  GPU Fleet Summary                                       │\n")
	fmt.Fprintf(w, "  ├──────────────────────────────────────────────────────────┤\n")
	fmt.Fprintf(w, "  │  Total GPU instances:      %-6d                        │\n", s.TotalInstances)
	fmt.Fprintf(w, "  │  Total monthly GPU spend:  $%-10.0f                   │\n", s.TotalMonthlyCost)
	fmt.Fprintf(w, "  │  Estimated monthly waste:  $%-10.0f (%4.0f%%)           │\n", s.TotalEstimatedWaste, s.WastePercent)
	fmt.Fprintf(w, "  └──────────────────────────────────────────────────────────┘\n\n")

	if s.TotalInstances == 0 {
		fmt.Fprintf(w, "  No GPU instances found.\n\n")
		return
	}

	// Group instances by severity
	critical, warning, healthy := groupBySeverity(result.Instances)

	if len(critical) > 0 {
		fmt.Fprintf(w, "  CRITICAL — %d instance(s), $%.0f/mo potential savings\n\n", len(critical), sumSavings(critical))
		printInstanceTable(w, critical)
	}

	if len(warning) > 0 {
		fmt.Fprintf(w, "  WARNING — %d instance(s), $%.0f/mo potential savings\n\n", len(warning), sumSavings(warning))
		printInstanceTable(w, warning)
	}

	if len(healthy) > 0 {
		fmt.Fprintf(w, "  OK — %d instance(s), $%.0f/mo (no issues detected)\n\n", len(healthy), sumCost(healthy))
	}
}

func printInstanceTable(w io.Writer, instances []models.GPUInstance) {
	// Header
	fmt.Fprintf(w, "  %-36s %-26s %10s  %-16s  %s\n",
		"Instance", "Type", "Monthly", "Signal", "Recommendation")
	fmt.Fprintf(w, "  %s %s %s  %s  %s\n",
		strings.Repeat("─", 36),
		strings.Repeat("─", 26),
		strings.Repeat("─", 10),
		strings.Repeat("─", 16),
		strings.Repeat("─", 50),
	)

	for _, inst := range instances {
		name := inst.Name
		if name == "" {
			name = inst.InstanceID
		}
		if len(name) > 34 {
			name = name[:31] + "..."
		}

		gpuDesc := fmt.Sprintf("%d× %s", inst.GPUCount, inst.GPUModel)
		typeDesc := fmt.Sprintf("%s (%s)", inst.InstanceType, gpuDesc)
		if len(typeDesc) > 26 {
			typeDesc = typeDesc[:23] + "..."
		}

		signal := ""
		if len(inst.WasteSignals) > 0 {
			signal = inst.WasteSignals[0].Type
		}

		rec := ""
		if len(inst.Recommendations) > 0 {
			rec = inst.Recommendations[0].Description
		}

		fmt.Fprintf(w, "  %-36s %-26s $%9.0f  %-16s  %s\n",
			name, typeDesc, inst.MonthlyCost, signal, rec)
	}
	fmt.Fprintln(w)
}

func groupBySeverity(instances []models.GPUInstance) (critical, warning, healthy []models.GPUInstance) {
	for _, inst := range instances {
		maxSev := maxSeverity(inst.WasteSignals)
		switch maxSev {
		case models.SeverityCritical:
			critical = append(critical, inst)
		case models.SeverityWarning:
			warning = append(warning, inst)
		default:
			healthy = append(healthy, inst)
		}
	}

	// Sort each group by savings descending
	sortBySavings := func(s []models.GPUInstance) {
		sort.Slice(s, func(i, j int) bool {
			return s[i].EstimatedSavings > s[j].EstimatedSavings
		})
	}
	sortBySavings(critical)
	sortBySavings(warning)

	return
}

func maxSeverity(signals []models.WasteSignal) models.Severity {
	max := models.Severity("")
	for _, s := range signals {
		if s.Severity == models.SeverityCritical {
			return models.SeverityCritical
		}
		if s.Severity == models.SeverityWarning {
			max = models.SeverityWarning
		}
		if s.Severity == models.SeverityInfo && max == "" {
			max = models.SeverityInfo
		}
	}
	return max
}

func sumSavings(instances []models.GPUInstance) float64 {
	total := 0.0
	for _, inst := range instances {
		total += inst.EstimatedSavings
	}
	return total
}

func sumCost(instances []models.GPUInstance) float64 {
	total := 0.0
	for _, inst := range instances {
		total += inst.MonthlyCost
	}
	return total
}
