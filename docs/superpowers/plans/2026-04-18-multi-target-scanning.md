# Multi-Target Scanning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable gpuaudit to scan multiple AWS accounts in a single invocation via STS AssumeRole, with optional Organizations auto-discovery.

**Architecture:** New `multiaccount.go` handles target resolution (explicit list or Organizations API) and credential assumption. The existing `Scan()` function is refactored to accept multiple targets and scan them all in parallel. Output formatters gain per-target summary sections when multiple targets are present. All new fields use `omitempty` so single-account scans produce identical output to today.

**Tech Stack:** Go 1.24, AWS SDK v2 (STS, Organizations), cobra CLI, standard library testing

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/providers/aws/multiaccount.go` | Create | `Target` struct, `ResolveTargets()`, `TargetError` type, STS AssumeRole + Organizations list |
| `internal/providers/aws/multiaccount_test.go` | Create | Tests for `ResolveTargets()` with mock STS/Org clients |
| `internal/models/models.go` | Modify | Add `TargetSummary`, `TargetErrorInfo` types; add new fields to `ScanResult` |
| `internal/providers/aws/scanner.go` | Modify | Refactor `Scan()` to use `ResolveTargets()` and scan all targets in parallel |
| `cmd/gpuaudit/main.go` | Modify | Add `--targets`, `--role`, `--org`, `--external-id`, `--skip-self` flags; wire into `ScanOptions` |
| `internal/providers/aws/summary.go` | Create | Extract `BuildSummary` from scanner.go, add `BuildTargetSummaries()` |
| `internal/providers/aws/summary_test.go` | Create | Tests for per-target summary computation |
| `internal/output/table.go` | Modify | Add "By Target" summary table and "Target" column when multiple targets |
| `internal/output/markdown.go` | Modify | Add per-target summary section when multiple targets |
| `internal/output/slack.go` | Modify | Add per-target summary block when multiple targets |
| `go.mod` | Modify | Add `organizations` SDK dependency |

---

### Task 1: Add model types for multi-target results

**Files:**
- Modify: `internal/models/models.go`

- [ ] **Step 1: Add `TargetSummary` and `TargetErrorInfo` types and new `ScanResult` fields**

Add to `internal/models/models.go` after the `ScanSummary` struct:

```go
// TargetSummary provides per-target aggregate statistics.
type TargetSummary struct {
	Target              string  `json:"target"`
	TotalInstances      int     `json:"total_instances"`
	TotalMonthlyCost    float64 `json:"total_monthly_cost"`
	TotalEstimatedWaste float64 `json:"total_estimated_waste"`
	WastePercent        float64 `json:"waste_percent"`
	CriticalCount       int     `json:"critical_count"`
	WarningCount        int     `json:"warning_count"`
}

// TargetErrorInfo describes a target that failed to scan.
type TargetErrorInfo struct {
	Target string `json:"target"`
	Error  string `json:"error"`
}
```

Add three new fields to `ScanResult`:

```go
type ScanResult struct {
	Timestamp       time.Time          `json:"timestamp"`
	AccountID       string             `json:"account_id"`
	Targets         []string           `json:"targets,omitempty"`
	Regions         []string           `json:"regions"`
	ScanDuration    string             `json:"scan_duration"`
	Instances       []GPUInstance      `json:"instances"`
	Summary         ScanSummary        `json:"summary"`
	TargetSummaries []TargetSummary    `json:"target_summaries,omitempty"`
	TargetErrors    []TargetErrorInfo  `json:"target_errors,omitempty"`
}
```

- [ ] **Step 2: Verify build passes**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go build ./...`
Expected: success (new types are additive, omitempty means no output change)

- [ ] **Step 3: Run existing tests to confirm nothing broke**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./...`
Expected: all pass

- [ ] **Step 4: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add internal/models/models.go
git commit -m "Add TargetSummary and TargetErrorInfo model types for multi-target scanning"
```

---

### Task 2: Extract `BuildSummary` and add `BuildTargetSummaries`

**Files:**
- Create: `internal/providers/aws/summary.go`
- Create: `internal/providers/aws/summary_test.go`
- Modify: `internal/providers/aws/scanner.go` (remove `BuildSummary` — it moves to summary.go)

- [ ] **Step 1: Write the failing test for `BuildTargetSummaries`**

Create `internal/providers/aws/summary_test.go`:

```go
package aws

import (
	"testing"

	"github.com/gpuaudit/cli/internal/models"
)

func TestBuildTargetSummaries_MultipleAccounts(t *testing.T) {
	instances := []models.GPUInstance{
		{
			AccountID:    "111111111111",
			MonthlyCost:  1000,
			EstimatedSavings: 500,
			WasteSignals: []models.WasteSignal{{Severity: models.SeverityCritical}},
		},
		{
			AccountID:    "111111111111",
			MonthlyCost:  2000,
			EstimatedSavings: 0,
		},
		{
			AccountID:    "222222222222",
			MonthlyCost:  3000,
			EstimatedSavings: 1000,
			WasteSignals: []models.WasteSignal{{Severity: models.SeverityWarning}},
		},
	}

	summaries := BuildTargetSummaries(instances)

	if len(summaries) != 2 {
		t.Fatalf("expected 2 target summaries, got %d", len(summaries))
	}

	// Find each target
	var s1, s2 *models.TargetSummary
	for i := range summaries {
		switch summaries[i].Target {
		case "111111111111":
			s1 = &summaries[i]
		case "222222222222":
			s2 = &summaries[i]
		}
	}

	if s1 == nil || s2 == nil {
		t.Fatal("missing target summaries")
	}

	if s1.TotalInstances != 2 {
		t.Errorf("acct1: expected 2 instances, got %d", s1.TotalInstances)
	}
	if s1.TotalMonthlyCost != 3000 {
		t.Errorf("acct1: expected $3000 cost, got $%.0f", s1.TotalMonthlyCost)
	}
	if s1.TotalEstimatedWaste != 500 {
		t.Errorf("acct1: expected $500 waste, got $%.0f", s1.TotalEstimatedWaste)
	}
	if s1.CriticalCount != 1 {
		t.Errorf("acct1: expected 1 critical, got %d", s1.CriticalCount)
	}

	if s2.TotalInstances != 1 {
		t.Errorf("acct2: expected 1 instance, got %d", s2.TotalInstances)
	}
	if s2.WarningCount != 1 {
		t.Errorf("acct2: expected 1 warning, got %d", s2.WarningCount)
	}
}

func TestBuildTargetSummaries_SingleAccount(t *testing.T) {
	instances := []models.GPUInstance{
		{AccountID: "111111111111", MonthlyCost: 1000},
	}

	summaries := BuildTargetSummaries(instances)

	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
}

func TestBuildTargetSummaries_Empty(t *testing.T) {
	summaries := BuildTargetSummaries(nil)

	if len(summaries) != 0 {
		t.Fatalf("expected 0 summaries for nil input, got %d", len(summaries))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./internal/providers/aws/ -run TestBuildTargetSummaries -v`
Expected: FAIL (function not defined)

- [ ] **Step 3: Create `summary.go` with `BuildSummary` (moved from scanner.go) and `BuildTargetSummaries`**

Create `internal/providers/aws/summary.go`:

```go
package aws

import (
	"sort"

	"github.com/gpuaudit/cli/internal/models"
)

// BuildSummary computes aggregate statistics for a set of GPU instances.
func BuildSummary(instances []models.GPUInstance) models.ScanSummary {
	s := models.ScanSummary{
		TotalInstances: len(instances),
	}

	for _, inst := range instances {
		s.TotalMonthlyCost += inst.MonthlyCost
		s.TotalEstimatedWaste += inst.EstimatedSavings

		maxSeverity := models.Severity("")
		for _, sig := range inst.WasteSignals {
			if sig.Severity == models.SeverityCritical {
				maxSeverity = models.SeverityCritical
			} else if sig.Severity == models.SeverityWarning && maxSeverity != models.SeverityCritical {
				maxSeverity = models.SeverityWarning
			} else if sig.Severity == models.SeverityInfo && maxSeverity == "" {
				maxSeverity = models.SeverityInfo
			}
		}

		switch maxSeverity {
		case models.SeverityCritical:
			s.CriticalCount++
		case models.SeverityWarning:
			s.WarningCount++
		case models.SeverityInfo:
			s.InfoCount++
		default:
			s.HealthyCount++
		}
	}

	if s.TotalMonthlyCost > 0 {
		s.WastePercent = (s.TotalEstimatedWaste / s.TotalMonthlyCost) * 100
	}

	return s
}

// BuildTargetSummaries computes per-target breakdowns from a flat instance list.
func BuildTargetSummaries(instances []models.GPUInstance) []models.TargetSummary {
	if len(instances) == 0 {
		return nil
	}

	byTarget := make(map[string][]models.GPUInstance)
	for _, inst := range instances {
		byTarget[inst.AccountID] = append(byTarget[inst.AccountID], inst)
	}

	summaries := make([]models.TargetSummary, 0, len(byTarget))
	for target, insts := range byTarget {
		ts := models.TargetSummary{
			Target:         target,
			TotalInstances: len(insts),
		}
		for _, inst := range insts {
			ts.TotalMonthlyCost += inst.MonthlyCost
			ts.TotalEstimatedWaste += inst.EstimatedSavings

			maxSev := models.Severity("")
			for _, sig := range inst.WasteSignals {
				if sig.Severity == models.SeverityCritical {
					maxSev = models.SeverityCritical
				} else if sig.Severity == models.SeverityWarning && maxSev != models.SeverityCritical {
					maxSev = models.SeverityWarning
				}
			}
			switch maxSev {
			case models.SeverityCritical:
				ts.CriticalCount++
			case models.SeverityWarning:
				ts.WarningCount++
			}
		}
		if ts.TotalMonthlyCost > 0 {
			ts.WastePercent = (ts.TotalEstimatedWaste / ts.TotalMonthlyCost) * 100
		}
		summaries = append(summaries, ts)
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].TotalMonthlyCost > summaries[j].TotalMonthlyCost
	})

	return summaries
}
```

- [ ] **Step 4: Remove `BuildSummary` and `matchesExcludeTags` from `scanner.go`**

In `internal/providers/aws/scanner.go`, delete the `BuildSummary` function (lines 235-272) and keep `matchesExcludeTags`. The `BuildSummary` is now in `summary.go`. No import changes needed since both files are in the same package.

- [ ] **Step 5: Run tests**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./... -v`
Expected: all pass, including the new `TestBuildTargetSummaries_*` tests

- [ ] **Step 6: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add internal/providers/aws/summary.go internal/providers/aws/summary_test.go internal/providers/aws/scanner.go
git commit -m "Extract BuildSummary to summary.go and add BuildTargetSummaries"
```

---

### Task 3: Implement `ResolveTargets` with STS AssumeRole

**Files:**
- Create: `internal/providers/aws/multiaccount.go`
- Create: `internal/providers/aws/multiaccount_test.go`
- Modify: `go.mod` (add organizations dependency)

- [ ] **Step 1: Add the Organizations SDK dependency**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go get github.com/aws/aws-sdk-go-v2/service/organizations`

- [ ] **Step 2: Write failing tests for `ResolveTargets`**

Create `internal/providers/aws/multiaccount_test.go`:

```go
package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type mockSTSClient struct {
	identity  *sts.GetCallerIdentityOutput
	roles     map[string]*sts.AssumeRoleOutput // keyed by account ID
	failAccts map[string]error
}

func (m *mockSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return m.identity, nil
}

func (m *mockSTSClient) AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	// Extract account ID from ARN: arn:aws:iam::<acct>:role/<name>
	arn := aws.ToString(params.RoleArn)
	// Simple extraction: find the account ID between the 4th and 5th colons
	acct := ""
	colons := 0
	for i, c := range arn {
		if c == ':' {
			colons++
			if colons == 4 {
				rest := arn[i+1:]
				for j, r := range rest {
					if r == ':' {
						acct = rest[:j]
						break
					}
				}
				break
			}
		}
	}
	if err, ok := m.failAccts[acct]; ok {
		return nil, err
	}
	if out, ok := m.roles[acct]; ok {
		return out, nil
	}
	return nil, fmt.Errorf("no role for account %s", acct)
}

type mockOrgClient struct {
	accounts []orgtypes.Account
}

func (m *mockOrgClient) ListAccounts(ctx context.Context, params *organizations.ListAccountsInput, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error) {
	return &organizations.ListAccountsOutput{Accounts: m.accounts}, nil
}

func TestResolveTargets_NoTargets_ReturnsSelf(t *testing.T) {
	stsClient := &mockSTSClient{
		identity: &sts.GetCallerIdentityOutput{Account: aws.String("999999999999")},
	}

	targets, errs := ResolveTargets(context.Background(), aws.Config{}, stsClient, nil, ScanOptions{})

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].AccountID != "999999999999" {
		t.Errorf("expected account 999999999999, got %s", targets[0].AccountID)
	}
}

func TestResolveTargets_ExplicitTargets(t *testing.T) {
	stsClient := &mockSTSClient{
		identity: &sts.GetCallerIdentityOutput{Account: aws.String("999999999999")},
		roles: map[string]*sts.AssumeRoleOutput{
			"111111111111": {Credentials: &ststypes.Credentials{
				AccessKeyId: aws.String("AK1"), SecretAccessKey: aws.String("SK1"), SessionToken: aws.String("ST1"),
			}},
			"222222222222": {Credentials: &ststypes.Credentials{
				AccessKeyId: aws.String("AK2"), SecretAccessKey: aws.String("SK2"), SessionToken: aws.String("ST2"),
			}},
		},
	}

	opts := ScanOptions{
		Targets: []string{"111111111111", "222222222222"},
		Role:    "gpuaudit-reader",
	}

	targets, errs := ResolveTargets(context.Background(), aws.Config{}, stsClient, nil, opts)

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	// 2 explicit + self = 3
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets (2 explicit + self), got %d", len(targets))
	}
}

func TestResolveTargets_ExplicitTargets_SkipSelf(t *testing.T) {
	stsClient := &mockSTSClient{
		identity: &sts.GetCallerIdentityOutput{Account: aws.String("999999999999")},
		roles: map[string]*sts.AssumeRoleOutput{
			"111111111111": {Credentials: &ststypes.Credentials{
				AccessKeyId: aws.String("AK1"), SecretAccessKey: aws.String("SK1"), SessionToken: aws.String("ST1"),
			}},
		},
	}

	opts := ScanOptions{
		Targets:  []string{"111111111111"},
		Role:     "gpuaudit-reader",
		SkipSelf: true,
	}

	targets, errs := ResolveTargets(context.Background(), aws.Config{}, stsClient, nil, opts)

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target (skip self), got %d", len(targets))
	}
	if targets[0].AccountID != "111111111111" {
		t.Errorf("expected 111111111111, got %s", targets[0].AccountID)
	}
}

func TestResolveTargets_PartialFailure(t *testing.T) {
	stsClient := &mockSTSClient{
		identity: &sts.GetCallerIdentityOutput{Account: aws.String("999999999999")},
		roles: map[string]*sts.AssumeRoleOutput{
			"111111111111": {Credentials: &ststypes.Credentials{
				AccessKeyId: aws.String("AK1"), SecretAccessKey: aws.String("SK1"), SessionToken: aws.String("ST1"),
			}},
		},
		failAccts: map[string]error{
			"222222222222": fmt.Errorf("AccessDenied"),
		},
	}

	opts := ScanOptions{
		Targets:  []string{"111111111111", "222222222222"},
		Role:     "gpuaudit-reader",
		SkipSelf: true,
	}

	targets, errs := ResolveTargets(context.Background(), aws.Config{}, stsClient, nil, opts)

	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].AccountID != "222222222222" {
		t.Errorf("expected error for 222222222222, got %s", errs[0].AccountID)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 successful target, got %d", len(targets))
	}
}

func TestResolveTargets_OrgDiscovery(t *testing.T) {
	stsClient := &mockSTSClient{
		identity: &sts.GetCallerIdentityOutput{Account: aws.String("999999999999")},
		roles: map[string]*sts.AssumeRoleOutput{
			"111111111111": {Credentials: &ststypes.Credentials{
				AccessKeyId: aws.String("AK1"), SecretAccessKey: aws.String("SK1"), SessionToken: aws.String("ST1"),
			}},
		},
	}

	orgClient := &mockOrgClient{
		accounts: []orgtypes.Account{
			{Id: aws.String("999999999999"), Status: orgtypes.AccountStatusActive},
			{Id: aws.String("111111111111"), Status: orgtypes.AccountStatusActive},
			{Id: aws.String("333333333333"), Status: orgtypes.AccountStatusSuspended},
		},
	}

	opts := ScanOptions{
		OrgScan: true,
		Role:    "gpuaudit-reader",
	}

	targets, errs := ResolveTargets(context.Background(), aws.Config{}, stsClient, orgClient, opts)

	// 999 (self, no assume) + 111 (assumed) = 2 targets; 333 is suspended so skipped
	// Note: 999 is self so not assumed; 111 is assumed successfully
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets (self + 1 active non-self), got %d", len(targets))
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./internal/providers/aws/ -run TestResolveTargets -v`
Expected: FAIL (function and types not defined)

- [ ] **Step 4: Implement `multiaccount.go`**

Create `internal/providers/aws/multiaccount.go`:

```go
package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
)

// Target represents a resolved scan target with its credentials.
type Target struct {
	AccountID string
	Config    aws.Config
}

// TargetError records a target that failed credential resolution.
type TargetError struct {
	AccountID string
	Err       error
}

// STSClient is the subset of the STS API we need.
type STSClient interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// OrgClient is the subset of the Organizations API we need.
type OrgClient interface {
	ListAccounts(ctx context.Context, params *organizations.ListAccountsInput, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error)
}

// ResolveTargets determines which accounts to scan and obtains credentials for each.
func ResolveTargets(ctx context.Context, baseCfg aws.Config, stsClient STSClient, orgClient OrgClient, opts ScanOptions) ([]Target, []TargetError) {
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, []TargetError{{AccountID: "unknown", Err: fmt.Errorf("GetCallerIdentity: %w", err)}}
	}
	selfAccount := aws.ToString(identity.Account)

	// No multi-target flags: return self only
	if len(opts.Targets) == 0 && !opts.OrgScan {
		return []Target{{AccountID: selfAccount, Config: baseCfg}}, nil
	}

	// Determine account IDs to scan
	var accountIDs []string
	if opts.OrgScan {
		discovered, err := discoverOrgAccounts(ctx, orgClient)
		if err != nil {
			return nil, []TargetError{{AccountID: "org", Err: fmt.Errorf("ListAccounts: %w", err)}}
		}
		accountIDs = discovered
	} else {
		accountIDs = opts.Targets
	}

	var targets []Target
	var targetErrors []TargetError

	// Include self unless skipped
	if !opts.SkipSelf {
		targets = append(targets, Target{AccountID: selfAccount, Config: baseCfg})
	}

	// Assume role in each non-self account
	for _, acctID := range accountIDs {
		if acctID == selfAccount {
			continue // already included as self (or skipped)
		}

		cfg, err := assumeRole(ctx, baseCfg, stsClient, acctID, opts.Role, opts.ExternalID)
		if err != nil {
			targetErrors = append(targetErrors, TargetError{AccountID: acctID, Err: err})
			continue
		}
		targets = append(targets, Target{AccountID: acctID, Config: cfg})
	}

	return targets, targetErrors
}

func discoverOrgAccounts(ctx context.Context, client OrgClient) ([]string, error) {
	var accounts []string
	var nextToken *string

	for {
		out, err := client.ListAccounts(ctx, &organizations.ListAccountsInput{
			NextToken: nextToken,
		})
		if err != nil {
			return nil, err
		}
		for _, acct := range out.Accounts {
			if acct.Status == orgtypes.AccountStatusActive {
				accounts = append(accounts, aws.ToString(acct.Id))
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	return accounts, nil
}

func assumeRole(ctx context.Context, baseCfg aws.Config, stsClient STSClient, accountID, roleName, externalID string) (aws.Config, error) {
	roleArn := fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)

	input := &sts.AssumeRoleInput{
		RoleArn:         &roleArn,
		RoleSessionName: aws.String("gpuaudit"),
	}
	if externalID != "" {
		input.ExternalId = &externalID
	}

	out, err := stsClient.AssumeRole(ctx, input)
	if err != nil {
		return aws.Config{}, fmt.Errorf("AssumeRole %s: %w", roleArn, err)
	}

	creds := out.Credentials
	cfg := baseCfg.Copy()
	cfg.Credentials = credentials.NewStaticCredentialsProvider(
		aws.ToString(creds.AccessKeyId),
		aws.ToString(creds.SecretAccessKey),
		aws.ToString(creds.SessionToken),
	)

	return cfg, nil
}
```

- [ ] **Step 5: Fix the test import — add `ststypes` import**

The tests reference `ststypes.Credentials`. Add this import to `multiaccount_test.go`:

```go
import (
	// ... existing imports ...
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
)
```

- [ ] **Step 6: Run tests**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./internal/providers/aws/ -run TestResolveTargets -v`
Expected: all pass

- [ ] **Step 7: Run full test suite**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./...`
Expected: all pass

- [ ] **Step 8: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add internal/providers/aws/multiaccount.go internal/providers/aws/multiaccount_test.go go.mod go.sum
git commit -m "Add ResolveTargets with STS AssumeRole and Organizations discovery"
```

---

### Task 4: Refactor `Scan()` for multi-target parallel scanning

**Files:**
- Modify: `internal/providers/aws/scanner.go`

- [ ] **Step 1: Add multi-target fields to `ScanOptions`**

In `internal/providers/aws/scanner.go`, add to the `ScanOptions` struct:

```go
type ScanOptions struct {
	Profile       string
	Regions       []string
	MetricWindow  MetricWindow
	SkipMetrics   bool
	SkipSageMaker bool
	SkipEKS       bool
	SkipCosts     bool
	ExcludeTags   map[string]string
	MinUptimeDays int

	// Multi-target options
	Targets    []string
	Role       string
	ExternalID string
	OrgScan    bool
	SkipSelf   bool
}
```

- [ ] **Step 2: Refactor `Scan()` to use `ResolveTargets` and scan all targets in parallel**

Replace the `Scan` function in `scanner.go` with:

```go
func Scan(ctx context.Context, opts ScanOptions) (*models.ScanResult, error) {
	start := time.Now()

	// Load AWS config
	cfgOpts := []func(*awsconfig.LoadOptions) error{}
	if opts.Profile != "" {
		cfgOpts = append(cfgOpts, awsconfig.WithSharedConfigProfile(opts.Profile))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	// Resolve targets
	stsClient := sts.NewFromConfig(cfg)
	var orgClient OrgClient
	if opts.OrgScan {
		orgClient = organizations.NewFromConfig(cfg)
	}

	targets, targetErrors := ResolveTargets(ctx, cfg, stsClient, orgClient, opts)
	if len(targets) == 0 {
		return nil, fmt.Errorf("no scannable targets resolved")
	}

	// Report target errors
	for _, te := range targetErrors {
		fmt.Fprintf(os.Stderr, "  warning: target %s: %v\n", te.AccountID, te.Err)
	}

	fmt.Fprintf(os.Stderr, "  Scanning %d target(s)...\n", len(targets))

	// Determine regions to scan
	regions := opts.Regions
	if len(regions) == 0 {
		regions, err = getGPURegions(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("listing regions: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "  Scanning %d regions per target for GPU instances...\n", len(regions))

	// Scan all targets in parallel
	type targetResult struct {
		accountID string
		instances []models.GPUInstance
		regions   []string
	}

	resultsCh := make(chan targetResult, len(targets))
	var wg sync.WaitGroup

	for _, target := range targets {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			instances, scannedRegions := scanTarget(ctx, t, regions, opts)
			resultsCh <- targetResult{
				accountID: t.AccountID,
				instances: instances,
				regions:   scannedRegions,
			}
		}(target)
	}

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	var allInstances []models.GPUInstance
	regionSet := make(map[string]bool)
	callerAccount := ""
	if len(targets) > 0 {
		callerAccount = targets[0].AccountID
	}

	for res := range resultsCh {
		allInstances = append(allInstances, res.instances...)
		for _, r := range res.regions {
			regionSet[r] = true
		}
	}

	var scannedRegions []string
	for r := range regionSet {
		scannedRegions = append(scannedRegions, r)
	}

	// Filter by excluded tags
	if len(opts.ExcludeTags) > 0 {
		filtered := allInstances[:0]
		excluded := 0
		for _, inst := range allInstances {
			if matchesExcludeTags(inst.Tags, opts.ExcludeTags) {
				excluded++
				continue
			}
			filtered = append(filtered, inst)
		}
		allInstances = filtered
		if excluded > 0 {
			fmt.Fprintf(os.Stderr, "  Excluded %d instance(s) by tag filter.\n", excluded)
		}
	}

	// Run analysis
	analysis.AnalyzeAll(allInstances)

	// Suppress signals below minimum uptime threshold
	if opts.MinUptimeDays > 0 {
		minHours := float64(opts.MinUptimeDays) * 24
		for i := range allInstances {
			inst := &allInstances[i]
			if inst.UptimeHours >= minHours {
				continue
			}
			inst.WasteSignals = nil
			inst.Recommendations = nil
			inst.EstimatedSavings = 0
		}
	}

	// Build summaries
	summary := BuildSummary(allInstances)

	result := &models.ScanResult{
		Timestamp:    start,
		AccountID:    callerAccount,
		Regions:      scannedRegions,
		ScanDuration: time.Since(start).Round(time.Millisecond).String(),
		Instances:    allInstances,
		Summary:      summary,
	}

	// Add multi-target metadata
	if len(targets) > 1 || len(targetErrors) > 0 {
		for _, t := range targets {
			result.Targets = append(result.Targets, t.AccountID)
		}
		result.TargetSummaries = BuildTargetSummaries(allInstances)
		for _, te := range targetErrors {
			result.TargetErrors = append(result.TargetErrors, models.TargetErrorInfo{
				Target: te.AccountID,
				Error:  te.Err.Error(),
			})
		}
	}

	return result, nil
}

// scanTarget scans all regions for a single target account.
func scanTarget(ctx context.Context, target Target, regions []string, opts ScanOptions) ([]models.GPUInstance, []string) {
	type regionResult struct {
		region    string
		instances []models.GPUInstance
		err       error
	}

	results := make(chan regionResult, len(regions))
	var wg sync.WaitGroup

	for _, region := range regions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			instances, err := scanRegion(ctx, target.Config, target.AccountID, r, opts)
			results <- regionResult{region: r, instances: instances, err: err}
		}(region)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var allInstances []models.GPUInstance
	var scannedRegions []string

	for res := range results {
		if res.err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %s/%s: %v\n", target.AccountID, res.region, res.err)
			continue
		}
		if len(res.instances) > 0 {
			allInstances = append(allInstances, res.instances...)
			scannedRegions = append(scannedRegions, res.region)
		}
	}

	// Enrich with Cost Explorer data (per-target, since CE is account-scoped)
	if !opts.SkipCosts && len(allInstances) > 0 {
		ceClient := costexplorer.NewFromConfig(target.Config)
		if err := EnrichCostData(ctx, ceClient, allInstances); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %s cost enrichment: %v\n", target.AccountID, err)
		}
	}

	return allInstances, scannedRegions
}
```

- [ ] **Step 3: Add the organizations import to scanner.go**

Add to the import block:

```go
"github.com/aws/aws-sdk-go-v2/service/organizations"
```

- [ ] **Step 4: Verify build passes**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go build ./...`
Expected: success

- [ ] **Step 5: Run all tests**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./...`
Expected: all pass

- [ ] **Step 6: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add internal/providers/aws/scanner.go
git commit -m "Refactor Scan() for parallel multi-target scanning"
```

---

### Task 5: Wire CLI flags into scan command

**Files:**
- Modify: `cmd/gpuaudit/main.go`

- [ ] **Step 1: Add flag variables and register flags**

Add the new flag variables alongside the existing scan flags:

```go
var (
	// ... existing flags ...
	scanTargets    []string
	scanRole       string
	scanExternalID string
	scanOrg        bool
	scanSkipSelf   bool
)
```

In the `init()` function, add after the existing `scanCmd.Flags` calls:

```go
scanCmd.Flags().StringSliceVar(&scanTargets, "targets", nil, "Account IDs to scan (comma-separated)")
scanCmd.Flags().StringVar(&scanRole, "role", "", "IAM role name to assume in each target")
scanCmd.Flags().StringVar(&scanExternalID, "external-id", "", "STS external ID for cross-account role assumption")
scanCmd.Flags().BoolVar(&scanOrg, "org", false, "Auto-discover all accounts from AWS Organizations")
scanCmd.Flags().BoolVar(&scanSkipSelf, "skip-self", false, "Exclude the caller's own account from the scan")
scanCmd.MarkFlagsMutuallyExclusive("targets", "org")
```

- [ ] **Step 2: Wire flags into `ScanOptions` in `runScan`**

In the `runScan` function, add the new fields to the opts construction:

```go
opts := awsprovider.DefaultScanOptions()
opts.Profile = scanProfile
opts.Regions = scanRegions
opts.SkipMetrics = scanSkipMetrics
opts.SkipSageMaker = scanSkipSageMaker
opts.SkipEKS = scanSkipEKS
opts.SkipCosts = scanSkipCosts
opts.ExcludeTags = parseExcludeTags(scanExcludeTags)
opts.MinUptimeDays = scanMinUptimeDays
opts.Targets = scanTargets
opts.Role = scanRole
opts.ExternalID = scanExternalID
opts.OrgScan = scanOrg
opts.SkipSelf = scanSkipSelf
```

- [ ] **Step 3: Add validation — `--role` required with `--targets` or `--org`**

Add at the top of `runScan`, before creating opts:

```go
if (len(scanTargets) > 0 || scanOrg) && scanRole == "" {
	return fmt.Errorf("--role is required when using --targets or --org")
}
```

- [ ] **Step 4: Verify build passes**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go build ./...`
Expected: success

- [ ] **Step 5: Verify CLI help shows new flags**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go run ./cmd/gpuaudit scan --help`
Expected: new flags visible in help text

- [ ] **Step 6: Run all tests**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./...`
Expected: all pass

- [ ] **Step 7: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add cmd/gpuaudit/main.go
git commit -m "Add --targets, --role, --org, --external-id, --skip-self flags to scan command"
```

---

### Task 6: Update table formatter for multi-target output

**Files:**
- Modify: `internal/output/table.go`

- [ ] **Step 1: Add "By Target" summary table to `FormatTable`**

In `internal/output/table.go`, add a new function and call it from `FormatTable` after the summary box:

```go
func printTargetSummary(w io.Writer, result *models.ScanResult) {
	if len(result.TargetSummaries) < 2 {
		return
	}

	fmt.Fprintf(w, "  By Target\n")
	fmt.Fprintf(w, "  ┌──────────────┬───────────┬───────────┬───────────┬───────┐\n")
	fmt.Fprintf(w, "  │ Target       │ Instances │ Spend/mo  │ Waste/mo  │ Waste │\n")
	fmt.Fprintf(w, "  ├──────────────┼───────────┼───────────┼───────────┼───────┤\n")
	for _, ts := range result.TargetSummaries {
		fmt.Fprintf(w, "  │ %-12s │ %9d │ $%8.0f │ $%8.0f │ %4.0f%% │\n",
			ts.Target, ts.TotalInstances, ts.TotalMonthlyCost,
			ts.TotalEstimatedWaste, ts.WastePercent)
	}
	fmt.Fprintf(w, "  └──────────────┴───────────┴───────────┴───────────┴───────┘\n\n")

	// Target errors
	if len(result.TargetErrors) > 0 {
		fmt.Fprintf(w, "  Warnings\n")
		for _, te := range result.TargetErrors {
			fmt.Fprintf(w, "  ✗ %s — %s\n", te.Target, te.Error)
		}
		fmt.Fprintln(w)
	}
}
```

In `FormatTable`, add the call after the summary box and before the "No GPU instances" check:

```go
// ... after the summary box closing line ...

printTargetSummary(w, result)

if s.TotalInstances == 0 {
```

- [ ] **Step 2: Add "Target" column to `printInstanceTable` when multi-target**

Modify `printInstanceTable` to accept and use target info. Since the formatter doesn't know if it's multi-target from just the instance slice, pass the result:

Change the call sites in `FormatTable` from:
```go
printInstanceTable(w, critical)
```
to:
```go
multiTarget := len(result.TargetSummaries) > 1
printInstanceTable(w, critical, multiTarget)
```

Update `printInstanceTable`:

```go
func printInstanceTable(w io.Writer, instances []models.GPUInstance, multiTarget bool) {
	if multiTarget {
		fmt.Fprintf(w, "  %-36s %-14s %-26s %10s  %-16s  %s\n",
			"Instance", "Target", "Type", "Monthly", "Signal", "Recommendation")
		fmt.Fprintf(w, "  %s %s %s %s  %s  %s\n",
			strings.Repeat("─", 36),
			strings.Repeat("─", 14),
			strings.Repeat("─", 26),
			strings.Repeat("─", 10),
			strings.Repeat("─", 16),
			strings.Repeat("─", 50),
		)
	} else {
		fmt.Fprintf(w, "  %-36s %-26s %10s  %-16s  %s\n",
			"Instance", "Type", "Monthly", "Signal", "Recommendation")
		fmt.Fprintf(w, "  %s %s %s  %s  %s\n",
			strings.Repeat("─", 36),
			strings.Repeat("─", 26),
			strings.Repeat("─", 10),
			strings.Repeat("─", 16),
			strings.Repeat("─", 50),
		)
	}

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

		if multiTarget {
			fmt.Fprintf(w, "  %-36s %-14s %-26s $%9.0f  %-16s  %s\n",
				name, inst.AccountID, typeDesc, inst.MonthlyCost, signal, rec)
		} else {
			fmt.Fprintf(w, "  %-36s %-26s $%9.0f  %-16s  %s\n",
				name, typeDesc, inst.MonthlyCost, signal, rec)
		}
	}
	fmt.Fprintln(w)
}
```

- [ ] **Step 3: Verify build passes**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go build ./...`
Expected: success

- [ ] **Step 4: Run all tests**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./...`
Expected: all pass

- [ ] **Step 5: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add internal/output/table.go
git commit -m "Add per-target summary table and target column to table formatter"
```

---

### Task 7: Update markdown and Slack formatters for multi-target output

**Files:**
- Modify: `internal/output/markdown.go`
- Modify: `internal/output/slack.go`

- [ ] **Step 1: Add per-target section to markdown formatter**

In `internal/output/markdown.go`, add after the Summary table (after the `s.HealthyCount` line and before the "No GPU instances" check):

```go
// Per-target breakdown
if len(result.TargetSummaries) > 1 {
	fmt.Fprintf(w, "## By Target\n\n")
	fmt.Fprintf(w, "| Target | Instances | Spend/mo | Waste/mo | Waste |\n")
	fmt.Fprintf(w, "|---|---|---|---|---|\n")
	for _, ts := range result.TargetSummaries {
		fmt.Fprintf(w, "| %s | %d | $%.0f | $%.0f | %.0f%% |\n",
			ts.Target, ts.TotalInstances, ts.TotalMonthlyCost,
			ts.TotalEstimatedWaste, ts.WastePercent)
	}
	fmt.Fprintln(w)
}

if len(result.TargetErrors) > 0 {
	fmt.Fprintf(w, "## Warnings\n\n")
	for _, te := range result.TargetErrors {
		fmt.Fprintf(w, "- **%s** — %s\n", te.Target, te.Error)
	}
	fmt.Fprintln(w)
}
```

Also add a "Target" column to the Findings table when multi-target. Change the table header and row formatting:

```go
if len(result.TargetSummaries) > 1 {
	fmt.Fprintf(w, "| Instance | Target | Type | Monthly Cost | Signal | Savings | Recommendation |\n")
	fmt.Fprintf(w, "|---|---|---|---|---|---|---|\n")
} else {
	fmt.Fprintf(w, "| Instance | Type | Monthly Cost | Signal | Savings | Recommendation |\n")
	fmt.Fprintf(w, "|---|---|---|---|---|---|\n")
}

for _, inst := range result.Instances {
	// ... existing name/signal/rec/savings formatting ...

	if len(result.TargetSummaries) > 1 {
		fmt.Fprintf(w, "| %s | %s | %s (%d× %s) | $%.0f | %s | %s | %s |\n",
			name, inst.AccountID, inst.InstanceType, inst.GPUCount, inst.GPUModel,
			inst.MonthlyCost, signal, savings, rec)
	} else {
		fmt.Fprintf(w, "| %s | %s (%d× %s) | $%.0f | %s | %s | %s |\n",
			name, inst.InstanceType, inst.GPUCount, inst.GPUModel,
			inst.MonthlyCost, signal, savings, rec)
	}
}
```

- [ ] **Step 2: Add per-target block to Slack formatter**

In `internal/output/slack.go`, in `FormatSlack`, add after the summary block and divider:

```go
// Per-target breakdown
if len(result.TargetSummaries) > 1 {
	lines := []string{"*By Target*"}
	for _, ts := range result.TargetSummaries {
		lines = append(lines, fmt.Sprintf("• `%s` — %d instances, $%.0f/mo spend, $%.0f/mo waste (%.0f%%)",
			ts.Target, ts.TotalInstances, ts.TotalMonthlyCost,
			ts.TotalEstimatedWaste, ts.WastePercent))
	}
	blocks = append(blocks, slackSection(strings.Join(lines, "\n")))
	blocks = append(blocks, map[string]any{"type": "divider"})
}

// Target errors
if len(result.TargetErrors) > 0 {
	lines := []string{":warning: *Target Warnings*"}
	for _, te := range result.TargetErrors {
		lines = append(lines, fmt.Sprintf("• `%s` — %s", te.Target, te.Error))
	}
	blocks = append(blocks, slackSection(strings.Join(lines, "\n")))
}
```

- [ ] **Step 3: Verify build passes**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go build ./...`
Expected: success

- [ ] **Step 4: Run all tests**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./...`
Expected: all pass

- [ ] **Step 5: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add internal/output/markdown.go internal/output/slack.go
git commit -m "Add per-target summaries to markdown and Slack formatters"
```

---

### Task 8: Update `iam-policy` command

**Files:**
- Modify: `cmd/gpuaudit/main.go`

- [ ] **Step 1: Add cross-account and Organizations statements to `iam-policy` output**

In `cmd/gpuaudit/main.go`, in the `iamPolicyCmd` Run function, add two new statements to the policy `Statement` slice:

```go
{
	"Sid":    "GPUAuditCrossAccount",
	"Effect": "Allow",
	"Action": "sts:AssumeRole",
	"Resource": "arn:aws:iam::*:role/gpuaudit-reader",
},
{
	"Sid":    "GPUAuditOrganizations",
	"Effect": "Allow",
	"Action": "organizations:ListAccounts",
	"Resource": "*",
},
```

Add a comment before encoding:

```go
fmt.Fprintln(os.Stdout, "// The last two statements (CrossAccount, Organizations) are only needed for --targets or --org scanning.")
```

- [ ] **Step 2: Verify build passes**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go build ./...`
Expected: success

- [ ] **Step 3: Verify output looks correct**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go run ./cmd/gpuaudit iam-policy`
Expected: JSON policy with the two new statements appended

- [ ] **Step 4: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add cmd/gpuaudit/main.go
git commit -m "Add cross-account and Organizations permissions to iam-policy output"
```

---

### Task 9: Update README with multi-target documentation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add multi-account scanning section to README**

Add a new section after the existing usage documentation:

```markdown
## Multi-Account Scanning

Scan multiple AWS accounts in a single invocation using STS AssumeRole.

### Prerequisites

Deploy a read-only IAM role (`gpuaudit-reader`) to each target account. See [Cross-Account Role Setup](#cross-account-role-setup) below.

### Usage

```bash
# Scan specific accounts
gpuaudit scan --targets 111111111111,222222222222 --role gpuaudit-reader

# Scan entire AWS Organization
gpuaudit scan --org --role gpuaudit-reader

# Exclude management account
gpuaudit scan --org --role gpuaudit-reader --skip-self

# With external ID
gpuaudit scan --targets 111111111111 --role gpuaudit-reader --external-id my-secret
```

### Cross-Account Role Setup

#### Terraform

```hcl
variable "management_account_id" {
  description = "AWS account ID where gpuaudit runs"
  type        = string
}

resource "aws_iam_role" "gpuaudit_reader" {
  name = "gpuaudit-reader"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::${var.management_account_id}:root" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "gpuaudit_reader" {
  name   = "gpuaudit-policy"
  role   = aws_iam_role.gpuaudit_reader.id
  policy = file("gpuaudit-policy.json")  # from: gpuaudit iam-policy > gpuaudit-policy.json
}
```

Deploy to all accounts using Terraform workspaces or CloudFormation StackSets.

#### CloudFormation StackSet

```yaml
AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  ManagementAccountId:
    Type: String
Resources:
  GpuAuditRole:
    Type: AWS::IAM::Role
    Properties:
      RoleName: gpuaudit-reader
      AssumeRolePolicyDocument:
        Version: "2012-10-17"
        Statement:
          - Effect: Allow
            Principal:
              AWS: !Sub "arn:aws:iam::${ManagementAccountId}:root"
            Action: sts:AssumeRole
      Policies:
        - PolicyName: gpuaudit-policy
          PolicyDocument:
            Version: "2012-10-17"
            Statement:
              - Effect: Allow
                Action:
                  - ec2:DescribeInstances
                  - ec2:DescribeInstanceTypes
                  - ec2:DescribeRegions
                  - sagemaker:ListEndpoints
                  - sagemaker:DescribeEndpoint
                  - sagemaker:DescribeEndpointConfig
                  - eks:ListClusters
                  - eks:ListNodegroups
                  - eks:DescribeNodegroup
                  - cloudwatch:GetMetricData
                  - cloudwatch:GetMetricStatistics
                  - cloudwatch:ListMetrics
                  - ce:GetCostAndUsage
                  - ce:GetReservationUtilization
                  - ce:GetSavingsPlansUtilization
                  - pricing:GetProducts
                Resource: "*"
```
```

- [ ] **Step 2: Commit**

```bash
cd /Users/smaksimov/Work/0cloud/gpuaudit
git add README.md
git commit -m "Add multi-account scanning docs to README"
```

---

### Task 10: End-to-end verification

**Files:** None (verification only)

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go test ./... -v`
Expected: all pass

- [ ] **Step 2: Run go vet**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go vet ./...`
Expected: no issues

- [ ] **Step 3: Verify CLI help**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go run ./cmd/gpuaudit scan --help`
Expected: all new flags visible (--targets, --role, --org, --external-id, --skip-self)

- [ ] **Step 4: Verify mutual exclusivity**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go run ./cmd/gpuaudit scan --targets 111 --org --role test 2>&1`
Expected: error about mutually exclusive flags

- [ ] **Step 5: Verify --role validation**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go run ./cmd/gpuaudit scan --targets 111 2>&1`
Expected: error "role is required when using --targets or --org"

- [ ] **Step 6: Verify single-account scan still works (no regression)**

Run: `cd /Users/smaksimov/Work/0cloud/gpuaudit && go run ./cmd/gpuaudit scan --skip-metrics --skip-sagemaker --skip-eks --skip-k8s --skip-costs 2>&1`
Expected: runs normally, output unchanged from before this feature
