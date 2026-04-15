// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/gpuaudit/cli/internal/diff"
	"github.com/gpuaudit/cli/internal/models"
)

// FormatDiffTable writes a human-readable diff report.
func FormatDiffTable(w io.Writer, d *diff.DiffResult) {
	fmt.Fprintf(w, "\n  gpuaudit diff — %s → %s\n\n", d.OldTimestamp, d.NewTimestamp)

	cs := d.CostSummary

	oldCount := len(d.Removed) + len(d.Changed) + d.UnchangedCount
	newCount := len(d.Added) + len(d.Changed) + d.UnchangedCount

	// Cost summary box
	boxWidth := 58 // inner width between │ markers
	boxLine := strings.Repeat("─", boxWidth)
	fmt.Fprintf(w, "  ┌%s┐\n", boxLine)
	writeBoxLine(w, "Cost Delta", boxWidth)
	fmt.Fprintf(w, "  ├%s┤\n", boxLine)
	writeBoxLine(w, fmt.Sprintf("Monthly spend:   $%-9.0f → $%-9.0f (%s)",
		cs.OldTotalMonthlyCost, cs.NewTotalMonthlyCost, diffFmtDelta(cs.CostChange)), boxWidth)
	writeBoxLine(w, fmt.Sprintf("Estimated waste: $%-9.0f → $%-9.0f (%s)",
		cs.OldTotalWaste, cs.NewTotalWaste, diffFmtDelta(cs.WasteChange)), boxWidth)
	writeBoxLine(w, fmt.Sprintf("Instances:       %d → %d  (-%d removed, +%d added)",
		oldCount, newCount, len(d.Removed), len(d.Added)), boxWidth)
	fmt.Fprintf(w, "  └%s┘\n", boxLine)

	// Removed
	if len(d.Removed) > 0 {
		sortInstancesByCost(d.Removed)
		fmt.Fprintf(w, "\n  REMOVED — %d instance(s), -$%.0f/mo\n\n", len(d.Removed), cs.RemovedSavings)
		printDiffInstanceTable(w, d.Removed)
	}

	// Added
	if len(d.Added) > 0 {
		sortInstancesByCost(d.Added)
		fmt.Fprintf(w, "\n  ADDED — %d instance(s), +$%.0f/mo\n\n", len(d.Added), cs.AddedCost)
		printDiffInstanceTable(w, d.Added)
	}

	// Changed
	if len(d.Changed) > 0 {
		fmt.Fprintf(w, "\n  CHANGED — %d instance(s)\n\n", len(d.Changed))
		fmt.Fprintf(w, "  %-36s  %s\n", "Instance", "Change")
		fmt.Fprintf(w, "  %s  %s\n", strings.Repeat("─", 36), strings.Repeat("─", 50))
		for _, c := range d.Changed {
			name := c.New.Name
			if name == "" {
				name = c.InstanceID
			}
			if len(name) > 34 {
				name = name[:31] + "..."
			}
			for i, change := range c.Changes {
				if i == 0 {
					fmt.Fprintf(w, "  %-36s  %s\n", name, change)
				} else {
					fmt.Fprintf(w, "  %-36s  %s\n", "", change)
				}
			}
		}
		fmt.Fprintln(w)
	}

	// Unchanged
	if d.UnchangedCount > 0 {
		fmt.Fprintf(w, "  UNCHANGED — %d instance(s)\n\n", d.UnchangedCount)
	}
}

func printDiffInstanceTable(w io.Writer, instances []models.GPUInstance) {
	fmt.Fprintf(w, "  %-36s %-26s %10s\n", "Instance", "Type", "Monthly")
	fmt.Fprintf(w, "  %s %s %s\n",
		strings.Repeat("─", 36), strings.Repeat("─", 26), strings.Repeat("─", 10))
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
		fmt.Fprintf(w, "  %-36s %-26s $%9.0f\n", name, typeDesc, inst.MonthlyCost)
	}
}

func sortInstancesByCost(instances []models.GPUInstance) {
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].MonthlyCost > instances[j].MonthlyCost
	})
}

func writeBoxLine(w io.Writer, content string, width int) {
	// Pad content to fill the box width (with 2-char margin on each side)
	inner := width - 4 // 2 spaces on each side
	if len(content) > inner {
		content = content[:inner]
	}
	fmt.Fprintf(w, "  │  %-*s  │\n", inner, content)
}

func diffFmtDelta(v float64) string {
	if v >= 0 {
		return fmt.Sprintf("+$%.0f", v)
	}
	return fmt.Sprintf("-$%.0f", -v)
}

// FormatDiffJSON writes the diff result as pretty-printed JSON.
func FormatDiffJSON(w io.Writer, d *diff.DiffResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(d)
}
