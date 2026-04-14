# Diff Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `gpuaudit diff old.json new.json` subcommand that compares two scan result JSON files and reports cost deltas, added/removed/changed instances.

**Architecture:** New `internal/diff/` package contains the comparison logic (`Compare` function). New `internal/output/diff.go` handles table and JSON formatting. The `diff` subcommand in `cmd/gpuaudit/main.go` reads two JSON files, calls `Compare`, and formats the output.

**Tech Stack:** Go standard library only — no new dependencies. Uses existing `models.ScanResult` and `models.GPUInstance` types.

---

### File Map

| File | Action | Responsibility |
|---|---|---|
| `internal/diff/diff.go` | Create | `DiffResult`, `InstanceDiff`, `CostDelta` types + `Compare` function |
| `internal/diff/diff_test.go` | Create | Unit tests for comparison logic |
| `internal/output/diff.go` | Create | `FormatDiffTable` and `FormatDiffJSON` formatters |
| `cmd/gpuaudit/main.go` | Modify | Register `diff` subcommand |

---

### Task 1: Core diff types and Compare function

**Files:**
- Create: `internal/diff/diff.go`
- Create: `internal/diff/diff_test.go`

- [ ] **Step 1: Write the test file with test for added instances**

Create `internal/diff/diff_test.go`:

```go
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
			TotalInstances:    len(instances),
			TotalMonthlyCost:  sumMonthlyCost(instances),
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./internal/diff/ -run TestCompare_AddedInstances -v`
Expected: FAIL — `Compare` not defined.

- [ ] **Step 3: Write the diff package with types and Compare function**

Create `internal/diff/diff.go`:

```go
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
	OldTimestamp    string             `json:"old_timestamp"`
	NewTimestamp    string             `json:"new_timestamp"`
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
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		changes = append(changes, fmt.Sprintf("Cost: $%.0f → $%.0f (%s$%.0f/mo)", old.MonthlyCost, new.MonthlyCost, sign, delta))
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./internal/diff/ -run TestCompare_AddedInstances -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add internal/diff/diff.go internal/diff/diff_test.go
git commit -m "Add diff package with Compare function and added-instance test"
```

---

### Task 2: Tests for removed, changed, unchanged, and cost math

**Files:**
- Modify: `internal/diff/diff_test.go`

- [ ] **Step 1: Add test for removed instances**

Append to `internal/diff/diff_test.go`:

```go
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
```

- [ ] **Step 2: Add test for changed instances (cost change)**

Append to `internal/diff/diff_test.go`:

```go
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
```

- [ ] **Step 3: Add test for changed instances (instance type, pricing model, state, GPU allocated, severity)**

Append to `internal/diff/diff_test.go`:

```go
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
```

- [ ] **Step 4: Add test for unchanged instances**

Append to `internal/diff/diff_test.go`:

```go
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
```

- [ ] **Step 5: Add test for cost summary math**

Append to `internal/diff/diff_test.go`:

```go
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
```

- [ ] **Step 6: Add test for empty scans**

Append to `internal/diff/diff_test.go`:

```go
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
```

- [ ] **Step 7: Run all tests**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./internal/diff/ -v`
Expected: All 6 tests PASS.

- [ ] **Step 8: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add internal/diff/diff_test.go
git commit -m "Add diff comparison tests: removed, changed, unchanged, cost math, empty"
```

---

### Task 3: Diff output formatters

**Files:**
- Create: `internal/output/diff.go`

- [ ] **Step 1: Create the diff output formatters**

Create `internal/output/diff.go`:

```go
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
	fmt.Fprintf(w, "  ┌──────────────────────────────────────────────────────────┐\n")
	fmt.Fprintf(w, "  │  Cost Delta                                              │\n")
	fmt.Fprintf(w, "  ├──────────────────────────────────────────────────────────┤\n")
	fmt.Fprintf(w, "  │  Monthly spend:   $%s → $%s  (%s)%s│\n",
		fmtCost(cs.OldTotalMonthlyCost), fmtCost(cs.NewTotalMonthlyCost),
		fmtDelta(cs.CostChange), pad(cs.OldTotalMonthlyCost, cs.NewTotalMonthlyCost, cs.CostChange))
	fmt.Fprintf(w, "  │  Estimated waste: $%s → $%s  (%s)%s│\n",
		fmtCost(cs.OldTotalWaste), fmtCost(cs.NewTotalWaste),
		fmtDelta(cs.WasteChange), pad(cs.OldTotalWaste, cs.NewTotalWaste, cs.WasteChange))
	fmt.Fprintf(w, "  │  Instances:       %d → %d  (-%d removed, +%d added)%s│\n",
		oldCount, newCount, len(d.Removed), len(d.Added),
		padInstances(oldCount, newCount, len(d.Removed), len(d.Added)))
	fmt.Fprintf(w, "  └──────────────────────────────────────────────────────────┘\n")

	// Removed
	if len(d.Removed) > 0 {
		sortByCost(d.Removed)
		fmt.Fprintf(w, "\n  REMOVED — %d instance(s), -$%.0f/mo\n\n", len(d.Removed), cs.RemovedSavings)
		printDiffInstanceTable(w, d.Removed)
	}

	// Added
	if len(d.Added) > 0 {
		sortByCost(d.Added)
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

func sortByCost(instances []models.GPUInstance) {
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].MonthlyCost > instances[j].MonthlyCost
	})
}

func fmtCost(v float64) string {
	return fmt.Sprintf("%.0f", v)
}

func fmtDelta(v float64) string {
	if v >= 0 {
		return fmt.Sprintf("+$%.0f", v)
	}
	return fmt.Sprintf("-$%.0f", -v)
}

// pad and padInstances return enough spaces to right-fill the summary box line to the closing │.
// These are best-effort; exact alignment depends on number widths.
func pad(old, new, delta float64) string {
	content := fmt.Sprintf("  │  Monthly spend:   $%s → $%s  (%s)",
		fmtCost(old), fmtCost(new), fmtDelta(delta))
	if len(content) >= 59 {
		return ""
	}
	return strings.Repeat(" ", 59-len(content))
}

func padInstances(oldCount, newCount, removed, added int) string {
	content := fmt.Sprintf("  │  Instances:       %d → %d  (-%d removed, +%d added)",
		oldCount, newCount, removed, added)
	if len(content) >= 59 {
		return ""
	}
	return strings.Repeat(" ", 59-len(content))
}

// FormatDiffJSON writes the diff result as pretty-printed JSON.
func FormatDiffJSON(w io.Writer, d *diff.DiffResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(d)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go build ./...`
Expected: Success (no errors).

- [ ] **Step 3: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add internal/output/diff.go
git commit -m "Add diff table and JSON output formatters"
```

---

### Task 4: Wire up the diff subcommand in main.go

**Files:**
- Modify: `cmd/gpuaudit/main.go:7-21` (imports)
- Modify: `cmd/gpuaudit/main.go:77-80` (init, register command)

- [ ] **Step 1: Add the diff subcommand to main.go**

Add import for the diff package. In the imports block at the top, add:

```go
"github.com/gpuaudit/cli/internal/diff"
```

Add these variables after the scan flag variables (after line 53, before `var scanCmd`):

```go
// --- diff command ---

var diffFormat string

var diffCmd = &cobra.Command{
	Use:   "diff <old.json> <new.json>",
	Short: "Compare two scan results and show what changed",
	Args:  cobra.ExactArgs(2),
	RunE:  runDiff,
}
```

Register the command in the first `init()` function, alongside the other `rootCmd.AddCommand` calls (line 78):

```go
rootCmd.AddCommand(diffCmd)
```

Add the flag registration in a new `init()` block or the existing one:

```go
func init() {
	diffCmd.Flags().StringVar(&diffFormat, "format", "table", "Output format: table, json")
}
```

Add the `runDiff` function after `runScan`:

```go
func runDiff(cmd *cobra.Command, args []string) error {
	old, err := loadScanResult(args[0])
	if err != nil {
		return fmt.Errorf("loading old scan: %w", err)
	}
	new, err := loadScanResult(args[1])
	if err != nil {
		return fmt.Errorf("loading new scan: %w", err)
	}

	result := diff.Compare(old, new)

	switch strings.ToLower(diffFormat) {
	case "json":
		return output.FormatDiffJSON(os.Stdout, result)
	default:
		output.FormatDiffTable(os.Stdout, result)
	}

	return nil
}

func loadScanResult(path string) (*models.ScanResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result models.ScanResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &result, nil
}
```

Note: `encoding/json` is already imported in main.go (used by iam-policy command).

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go build ./...`
Expected: Success.

- [ ] **Step 3: Run all tests**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./... -v`
Expected: All tests pass (including existing tests and new diff tests).

- [ ] **Step 4: Run go vet**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go vet ./...`
Expected: Clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add cmd/gpuaudit/main.go
git commit -m "Add diff subcommand to compare two scan results

Closes #5"
```

---

### Task 5: Manual smoke test

- [ ] **Step 1: Create two test JSON files and run diff**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit

cat > /tmp/old-scan.json << 'EOF'
{
  "timestamp": "2026-04-08T12:00:00Z",
  "account_id": "123456789",
  "regions": ["us-east-1"],
  "scan_duration": "5s",
  "instances": [
    {
      "instance_id": "i-aaa",
      "source": "k8s-node",
      "region": "us-east-1",
      "name": "ml-prod/node-1",
      "instance_type": "g6e.16xlarge",
      "gpu_model": "L40S",
      "gpu_count": 1,
      "state": "ready",
      "launch_time": "2026-03-01T00:00:00Z",
      "uptime_hours": 912,
      "pricing_model": "on-demand",
      "hourly_cost": 9.25,
      "monthly_cost": 6750,
      "gpu_allocated": 0,
      "estimated_savings": 6750,
      "waste_signals": [{"type": "idle", "severity": "critical", "confidence": 0.9, "evidence": "No GPU pods"}],
      "recommendations": [{"action": "terminate", "description": "Remove idle node", "current_monthly_cost": 6750, "monthly_savings": 6750, "savings_percent": 100, "risk": "low"}]
    },
    {
      "instance_id": "i-bbb",
      "source": "k8s-node",
      "region": "us-east-1",
      "name": "ml-prod/node-2",
      "instance_type": "g6e.16xlarge",
      "gpu_model": "L40S",
      "gpu_count": 1,
      "state": "ready",
      "launch_time": "2026-03-01T00:00:00Z",
      "uptime_hours": 912,
      "pricing_model": "on-demand",
      "hourly_cost": 9.25,
      "monthly_cost": 6750,
      "gpu_allocated": 1,
      "estimated_savings": 0
    }
  ],
  "summary": {
    "total_instances": 2,
    "total_monthly_cost": 13500,
    "total_estimated_waste": 6750,
    "waste_percent": 50,
    "critical_count": 1,
    "warning_count": 0,
    "info_count": 0,
    "healthy_count": 1
  }
}
EOF

cat > /tmp/new-scan.json << 'EOF'
{
  "timestamp": "2026-04-14T12:00:00Z",
  "account_id": "123456789",
  "regions": ["us-east-1"],
  "scan_duration": "4s",
  "instances": [
    {
      "instance_id": "i-bbb",
      "source": "k8s-node",
      "region": "us-east-1",
      "name": "ml-prod/node-2",
      "instance_type": "g6e.16xlarge",
      "gpu_model": "L40S",
      "gpu_count": 1,
      "state": "ready",
      "launch_time": "2026-03-01T00:00:00Z",
      "uptime_hours": 1056,
      "pricing_model": "on-demand",
      "hourly_cost": 9.25,
      "monthly_cost": 6750,
      "gpu_allocated": 1,
      "estimated_savings": 0
    },
    {
      "instance_id": "i-ccc",
      "source": "k8s-node",
      "region": "us-east-1",
      "name": "ml-prod/node-3",
      "instance_type": "g6.2xlarge",
      "gpu_model": "L4",
      "gpu_count": 1,
      "state": "ready",
      "launch_time": "2026-04-10T00:00:00Z",
      "uptime_hours": 96,
      "pricing_model": "on-demand",
      "hourly_cost": 1.23,
      "monthly_cost": 898,
      "gpu_allocated": 1,
      "estimated_savings": 0
    }
  ],
  "summary": {
    "total_instances": 2,
    "total_monthly_cost": 7648,
    "total_estimated_waste": 0,
    "waste_percent": 0,
    "critical_count": 0,
    "warning_count": 0,
    "info_count": 0,
    "healthy_count": 2
  }
}
EOF

go run ./cmd/gpuaudit diff /tmp/old-scan.json /tmp/new-scan.json
```

Expected: Table output showing i-aaa removed (-$6,750/mo), i-ccc added (+$898/mo), i-bbb unchanged. Cost summary showing $13,500 → $7,648 (-$5,852).

- [ ] **Step 2: Test JSON output**

```bash
go run ./cmd/gpuaudit diff /tmp/old-scan.json /tmp/new-scan.json --format json
```

Expected: JSON output with `added`, `removed`, `changed`, `unchanged_count`, and `cost_summary` fields.

- [ ] **Step 3: Clean up test files**

```bash
rm /tmp/old-scan.json /tmp/new-scan.json
```
