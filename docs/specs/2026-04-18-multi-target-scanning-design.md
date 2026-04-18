# Multi-Target Scanning

**Date:** April 18, 2026
**Status:** Draft

---

## Summary

Add the ability to scan multiple AWS accounts (and eventually GCP projects / Azure subscriptions) in a single `gpuaudit scan` invocation. Uses STS AssumeRole to obtain credentials for each target, scans them all in parallel, and merges results into a single flat output with per-target sub-summaries.

Zero breaking changes — existing single-account behavior is the default.

---

## CLI Interface

### New flags on `gpuaudit scan`

| Flag | Type | Description |
|------|------|-------------|
| `--targets` | `[]string` | Comma-separated list of account IDs to scan |
| `--role` | `string` | IAM role name to assume in each target (required with `--targets` or `--org`) |
| `--org` | `bool` | Auto-discover all accounts from AWS Organizations |
| `--external-id` | `string` | STS external ID for cross-account role assumption (optional) |
| `--skip-self` | `bool` | Exclude the caller's own account from the scan |

### Constraints

- `--targets` and `--org` are mutually exclusive.
- `--role` is required when `--targets` or `--org` is set.
- No `--targets` or `--org` means scan the caller's account only (current behavior, no changes).
- The caller's own account is included by default unless `--skip-self` is set.

### Examples

```bash
# Current behavior (unchanged)
gpuaudit scan

# Scan 3 specific accounts
gpuaudit scan --targets 111111111111,222222222222,333333333333 --role gpuaudit-reader

# Scan entire AWS Organization
gpuaudit scan --org --role gpuaudit-reader

# Org scan, exclude management account
gpuaudit scan --org --role gpuaudit-reader --skip-self

# With external ID for extra security
gpuaudit scan --targets 111111111111 --role gpuaudit-reader --external-id my-secret
```

### Flag naming rationale

Flags use provider-neutral names (`--targets` not `--accounts`, `--role` not `--assume-role`) so that when GCP and Azure support lands, the same flags work: targets are project IDs or subscription IDs, role is a service account or principal name. No renaming, no backward-compatibility concerns.

---

## Architecture

### New file: `internal/providers/aws/multiaccount.go`

Contains:

- `Target` struct: `{AccountID string, Config aws.Config}`
- `ResolveTargets(ctx, cfg, opts) ([]Target, []TargetError)`:
  - No `--targets`/`--org`: returns caller's account with existing config.
  - `--targets`: calls `sts:AssumeRole` for each account ID, returns credentials. Failed assumptions are collected as `TargetError`, not fatal.
  - `--org`: calls `organizations:ListAccounts`, filters to active accounts, then assumes role in each.
  - Caller's own account is included (with original config, no AssumeRole needed) unless `--skip-self`.
- `TargetError` struct: `{AccountID string, Err error}`

### Changes to `ScanOptions`

```go
type ScanOptions struct {
    // ... existing fields ...
    Targets    []string  // account IDs to scan
    Role       string    // role name to assume
    ExternalID string    // STS external ID
    OrgScan    bool      // auto-discover from Organizations
    SkipSelf   bool      // exclude caller's account
}
```

### Changes to `Scan()`

Current flow:
```
load config → get account ID → scan regions in parallel → merge → analyze → output
```

New flow:
```
load config → ResolveTargets() → for each target (parallel):
    for each region (parallel):
        scanRegion(ctx, target.Config, target.AccountID, region, opts)
→ merge all instances into flat list
→ filter, analyze, enrich (unchanged)
→ BuildSummary (global + per-target sub-summaries)
→ output
```

All targets are scanned in parallel. Within each target, all regions are scanned in parallel (same as today).

### Error handling: best-effort

- `ResolveTargets` returns both successful targets and a list of `TargetError`s.
- Scan continues for all resolvable targets.
- Per-region errors within a target are handled as today (warn and continue).
- Target-level errors are surfaced in the output (see Output section).
- Exit code: 0 = success, non-zero if all targets failed.

### Unchanged components

- Analysis rules — operate per-instance, already provider-agnostic.
- Diff command — matches by `instance_id`, globally unique across accounts.
- `GPUInstance` model — already has `AccountID` field.
- Pricing database — account-independent.

---

## Model Changes

### `ScanResult`

```go
type ScanResult struct {
    Timestamp       time.Time          `json:"timestamp"`
    AccountID       string             `json:"account_id"`              // caller's account (kept for backward compat)
    Targets         []string           `json:"targets,omitempty"`       // NEW: all scanned target IDs
    Regions         []string           `json:"regions"`
    ScanDuration    string             `json:"scan_duration"`
    Instances       []GPUInstance      `json:"instances"`
    Summary         ScanSummary        `json:"summary"`
    TargetSummaries []TargetSummary    `json:"target_summaries,omitempty"` // NEW: per-target breakdown
    TargetErrors    []TargetErrorInfo  `json:"target_errors,omitempty"`    // NEW: failed targets
}

type TargetSummary struct {
    Target             string  `json:"target"`
    TotalInstances     int     `json:"total_instances"`
    TotalMonthlyCost   float64 `json:"total_monthly_cost"`
    TotalEstimatedWaste float64 `json:"total_estimated_waste"`
    WastePercent       float64 `json:"waste_percent"`
    CriticalCount      int     `json:"critical_count"`
    WarningCount       int     `json:"warning_count"`
}

type TargetErrorInfo struct {
    Target string `json:"target"`
    Error  string `json:"error"`
}
```

New fields use `omitempty` — single-account scans produce identical JSON to today.

---

## Output Changes

### Table

When multiple targets are present, two additions:

1. **"By Target" summary table** after the global summary:

```
  By Target
  ┌──────────────┬───────────┬───────────┬───────────┬───────┐
  │ Target       │ Instances │ Spend/mo  │ Waste/mo  │ Waste │
  ├──────────────┼───────────┼───────────┼───────────┼───────┤
  │ 111111111111 │        31 │  $142,000 │   $38,000 │   27% │
  │ 222222222222 │        12 │   $35,400 │    $4,200 │   12% │
  └──────────────┴───────────┴───────────┴───────────┴───────┘
```

2. **"Target" column** in instance detail tables.

Single-target scans look identical to today.

### JSON

New `targets`, `target_summaries`, and `target_errors` fields as shown in the model above. Omitted when empty.

### Markdown

Per-target summary section added when multiple targets present.

### Slack

Per-target summary block added when multiple targets present.

### Errors

When targets fail, a warnings section appears in all formats:

```
  Warnings
  ✗ 444444444444 — AssumeRole failed: AccessDenied
  ✗ 555555555555 — role "gpuaudit-reader" not found in account
```

---

## IAM Policy Updates

### `gpuaudit iam-policy` additions

Add two new statements to the generated policy:

```json
{
    "Sid": "GPUAuditCrossAccount",
    "Effect": "Allow",
    "Action": "sts:AssumeRole",
    "Resource": "arn:aws:iam::*:role/gpuaudit-reader"
},
{
    "Sid": "GPUAuditOrganizations",
    "Effect": "Allow",
    "Action": "organizations:ListAccounts",
    "Resource": "*"
}
```

These are printed as a separate "Multi-Account Permissions" section in the `iam-policy` output, with a comment explaining they're only needed for `--targets` or `--org` scanning. Always included in the output — users can ignore them if they only scan a single account.

---

## Cross-Account Role Setup

### Terraform

```hcl
variable "management_account_id" {
  description = "AWS account ID where gpuaudit runs"
  type        = string
}

variable "external_id" {
  description = "External ID for AssumeRole (optional but recommended)"
  type        = string
  default     = ""
}

resource "aws_iam_role" "gpuaudit_reader" {
  name = "gpuaudit-reader"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::${var.management_account_id}:root" }
      Action    = "sts:AssumeRole"
      Condition = var.external_id != "" ? {
        StringEquals = { "sts:ExternalId" = var.external_id }
      } : {}
    }]
  })
}

resource "aws_iam_role_policy" "gpuaudit_reader" {
  name = "gpuaudit-policy"
  role = aws_iam_role.gpuaudit_reader.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "EC2ReadOnly"
        Effect   = "Allow"
        Action   = ["ec2:DescribeInstances", "ec2:DescribeInstanceTypes", "ec2:DescribeRegions"]
        Resource = "*"
      },
      {
        Sid      = "SageMakerReadOnly"
        Effect   = "Allow"
        Action   = ["sagemaker:ListEndpoints", "sagemaker:DescribeEndpoint", "sagemaker:DescribeEndpointConfig"]
        Resource = "*"
      },
      {
        Sid      = "EKSReadOnly"
        Effect   = "Allow"
        Action   = ["eks:ListClusters", "eks:ListNodegroups", "eks:DescribeNodegroup"]
        Resource = "*"
      },
      {
        Sid      = "CloudWatchReadOnly"
        Effect   = "Allow"
        Action   = ["cloudwatch:GetMetricData", "cloudwatch:GetMetricStatistics", "cloudwatch:ListMetrics"]
        Resource = "*"
      },
      {
        Sid      = "CostExplorerReadOnly"
        Effect   = "Allow"
        Action   = ["ce:GetCostAndUsage", "ce:GetReservationUtilization", "ce:GetSavingsPlansUtilization"]
        Resource = "*"
      },
      {
        Sid      = "PricingReadOnly"
        Effect   = "Allow"
        Action   = ["pricing:GetProducts"]
        Resource = "*"
      }
    ]
  })
}
```

### CloudFormation (for StackSet deployment across all accounts)

```yaml
AWSTemplateFormatVersion: "2010-09-09"
Description: gpuaudit cross-account reader role

Parameters:
  ManagementAccountId:
    Type: String
    Description: Account ID where gpuaudit runs
  ExternalId:
    Type: String
    Description: External ID for AssumeRole
    Default: ""

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

Recommended deployment: use CloudFormation StackSets to deploy the role to all member accounts from the management account.

---

## Testing

- **Unit tests for `ResolveTargets`**: mock STS and Organizations clients, verify correct target list for each mode (explicit, org, skip-self, mixed failures).
- **Unit tests for `BuildSummary`**: verify per-target summaries compute correctly with instances from multiple accounts.
- **Unit tests for output formatters**: verify "By Target" table and Target column appear only when multiple targets present.
- **Integration test pattern**: test the full `Scan` flow with mocked AWS clients for 2-3 accounts, verify merged output.
