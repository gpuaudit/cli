# gpuaudit diff — Historical Scan Comparison

**Issue:** #5
**Date:** 2026-04-14

## Problem

After running `gpuaudit scan --format json` periodically, there's no way to compare two reports and see what changed. When rescanning infrastructure (e.g. after filing a Karpenter waste escalation), you can't tell at a glance whether the situation improved.

## Solution

New `gpuaudit diff old.json new.json` subcommand that loads two scan result JSON files, matches instances by `instance_id`, and produces a cost-focused delta report.

## Data Model

### DiffResult

```go
// internal/diff/diff.go

type DiffResult struct {
    OldTimestamp    time.Time
    NewTimestamp    time.Time
    Added          []models.GPUInstance  // in new, not in old
    Removed        []models.GPUInstance  // in old, not in new
    Changed        []InstanceDiff        // in both, something changed
    UnchangedCount int
    CostSummary    CostDelta
}

type InstanceDiff struct {
    InstanceID string
    Old        models.GPUInstance
    New        models.GPUInstance
    CostDelta  float64   // new.MonthlyCost - old.MonthlyCost
    Changes    []string  // human-readable: "GPU allocated: 0 -> 2"
}

type CostDelta struct {
    OldTotalMonthlyCost float64
    NewTotalMonthlyCost float64
    CostChange          float64
    OldTotalWaste       float64
    NewTotalWaste       float64
    WasteChange         float64
    AddedCost           float64  // cost from new instances
    RemovedSavings      float64  // cost removed with departing instances
}
```

### Instance Matching

Instances are matched by `instance_id`. If an instance ID exists only in old, it's "removed". Only in new, it's "added". In both, it's compared for changes.

No fuzzy matching by name or instance type — a replaced node is honestly reported as removed + added.

### Change Detection

An instance is "changed" if any of these fields differ between old and new:

| Field | Format |
|---|---|
| `InstanceType` | `Instance type: g6e.16xlarge -> g6e.48xlarge` |
| `PricingModel` | `Pricing: on-demand -> reserved` |
| `MonthlyCost` | `Cost: $6,750 -> $4,200 (-$2,550/mo)` |
| `State` | `State: ready -> not-ready` |
| `GPUAllocated` | `GPU allocated: 0 -> 2` |
| `WasteSignals` | `Severity: critical -> (none)` or `Signal: idle -> low_utilization` |

If none of these differ, the instance is counted as unchanged (not listed).

## Table Output Format

```
  gpuaudit diff -- Apr 08 -> Apr 14

  +----------------------------------------------------------+
  |  Cost Delta                                              |
  +----------------------------------------------------------+
  |  Monthly spend:   $372,000 -> $251,000  (-$121,000)     |
  |  Estimated waste: $189,000 -> $68,000   (-$121,000)     |
  |  Instances:       116 -> 82  (-34 removed, +0 added)    |
  +----------------------------------------------------------+

  REMOVED -- 34 instance(s), -$121,000/mo

  Instance                             Type                       Monthly
  ------------------------------------ -------------------------- ----------
  gpu-cluster/ip-10-22-249-9           g6e.48xlarge (8x L40S)    $26,800
  ...

  ADDED -- 2 instance(s), +$5,000/mo

  Instance                             Type                       Monthly
  ------------------------------------ -------------------------- ----------
  ...

  CHANGED -- 3 instance(s)

  Instance                             Change
  ------------------------------------ ------------------------------------------
  gpu-cluster/ip-10-1-2-3              GPU allocated: 0 -> 2 (was idle)
  gpu-cluster/ip-10-4-5-6              Pricing: on-demand -> reserved (-$2,400/mo)

  UNCHANGED -- 77 instance(s)
```

Cost summary box is the first thing rendered — the "did it get better" answer. Sections only appear if non-empty.

## JSON Output Format

Serialize `DiffResult` as JSON for programmatic consumption. Same structure as the Go types above.

## CLI Interface

```
gpuaudit diff <old.json> <new.json> [--format table|json]
```

- Two required positional arguments (file paths to JSON scan results)
- `--format` flag, default `table`
- Exit code 0 always (diff is informational, not pass/fail)

## Files

### Create

- `internal/diff/diff.go` — `Compare(old, new *models.ScanResult) *DiffResult` plus `computeCostDelta` and `diffInstance` helpers
- `internal/diff/diff_test.go` — tests: added, removed, changed (each field), unchanged, cost math, empty scans
- `internal/output/diff.go` — `FormatDiffTable(w, *DiffResult)` and `FormatDiffJSON(w, *DiffResult)`

### Modify

- `cmd/gpuaudit/main.go` — register `diff` subcommand with two positional args and `--format` flag

## Testing

- Unit tests in `diff_test.go` covering: add/remove/change detection, all 6 compared fields, cost delta math, edge cases (empty old, empty new, identical scans)
- Manual test: run two scans against same cluster, diff the output files
