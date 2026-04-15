# gpuaudit

Scan your cloud for GPU waste and get actionable recommendations to cut your spend.

```
$ gpuaudit scan --skip-eks

  Found 103 GPU nodes across 111 nodes in gpu-cluster

  gpuaudit — GPU Cost Audit for AWS
  Account: 123456789012 | Regions: us-east-1 | Duration: 4.2s

  ┌──────────────────────────────────────────────────────────┐
  │  GPU Fleet Summary                                       │
  ├──────────────────────────────────────────────────────────┤
  │  Total GPU instances:      103                           │
  │  Total monthly GPU spend:  $365155                       │
  │  Estimated monthly waste:  $23408      (   6%)           │
  └──────────────────────────────────────────────────────────┘

  CRITICAL — 4 instance(s), $21728/mo potential savings

  Instance                             Type                       Monthly  Signal            Recommendation
  ──────────────────────────────────── ────────────────────────── ────────  ────────────────  ──────────────────────────────────────────────
  gpu-cluster/ip-10-15-255-248         g6e.16xlarge (1× L40S)     $  6752  idle              Node up 13 days with 0 GPU pods scheduled.
  gpu-cluster/ip-10-22-250-15          g6e.16xlarge (1× L40S)     $  6752  idle              Node up 1 days with 0 GPU pods scheduled.
  ...
```

## What it scans

- **EC2** — GPU instances (g4dn, g5, g6, g6e, p4d, p4de, p5, inf2, trn1) with CloudWatch metrics
- **SageMaker** — Endpoints with GPU utilization and invocation metrics
- **EKS** — Managed GPU node groups via the AWS EKS API
- **Kubernetes** — GPU nodes and pod allocation via the Kubernetes API (Karpenter, self-managed, any CNI)

## What it detects

- **Idle GPU instances** — running but doing nothing (low CPU + near-zero network for 24+ hours)
- **Oversized GPU** — multi-GPU instances where utilization suggests a single GPU would suffice
- **Pricing mismatch** — on-demand instances running 30+ days that should be Reserved Instances
- **Stale instances** — non-production instances running 90+ days
- **SageMaker low utilization** — endpoints with <10% GPU utilization
- **SageMaker oversized** — endpoints using <30% GPU memory on multi-GPU instances
- **K8s unallocated GPUs** — nodes with GPU capacity but no pods requesting GPUs

## Install

```bash
go install github.com/gpuaudit/cli/cmd/gpuaudit@latest
```

Or build from source:

```bash
git clone https://github.com/gpuaudit/cli.git
cd cli
go build -o gpuaudit ./cmd/gpuaudit
```

## Quick start

```bash
# Uses default AWS credentials (~/.aws/credentials or environment variables)
gpuaudit scan

# Specific profile and region
gpuaudit scan --profile production --region us-east-1

# Kubernetes cluster scan (uses KUBECONFIG or ~/.kube/config)
gpuaudit scan --skip-eks

# Specific kubeconfig and context
gpuaudit scan --kubeconfig ~/.kube/config --kube-context gpu-cluster

# JSON output for automation
gpuaudit scan --format json -o report.json

# Compare two scans to see what changed
gpuaudit diff old-report.json new-report.json

# Slack Block Kit payload (pipe to webhook)
gpuaudit scan --format slack -o - | \
  curl -X POST -H 'Content-Type: application/json' -d @- $SLACK_WEBHOOK

# Skip specific scanners
gpuaudit scan --skip-metrics    # faster, less accurate
gpuaudit scan --skip-sagemaker
gpuaudit scan --skip-eks        # skip AWS EKS API (use --skip-k8s for Kubernetes API)
gpuaudit scan --skip-k8s
```

## Comparing scans

Save scan results as JSON, then diff them later:

```bash
gpuaudit scan --format json -o scan-apr-08.json
# ... time passes, changes happen ...
gpuaudit scan --format json -o scan-apr-15.json
gpuaudit diff scan-apr-08.json scan-apr-15.json
```

```
  gpuaudit diff — 2026-04-08 12:00 UTC → 2026-04-15 12:00 UTC

  ┌──────────────────────────────────────────────────────────┐
  │  Cost Delta                                              │
  ├──────────────────────────────────────────────────────────┤
  │  Monthly spend:   $372000    → $365155    (-$6845)       │
  │  Estimated waste: $189000    → $23408     (-$165592)     │
  │  Instances:       116 → 103  (-13 removed, +0 added)    │
  └──────────────────────────────────────────────────────────┘

  REMOVED — 13 instance(s), -$6845/mo
  ...
```

Matches instances by ID. Reports added, removed, and changed instances with per-field diffs (instance type, pricing model, cost, state, GPU allocation, waste severity).

## IAM permissions

gpuaudit is read-only. It never modifies your infrastructure. Generate the minimal IAM policy:

```bash
gpuaudit iam-policy
```

For Kubernetes scanning, gpuaudit needs `get`/`list` on `nodes` and `pods` cluster-wide.

## GPU pricing reference

```bash
# List all GPU instance pricing
gpuaudit pricing

# Filter by GPU model
gpuaudit pricing --gpu H100
gpuaudit pricing --gpu L4
```

## Output formats

| Format | Flag | Use case |
|---|---|---|
| Table | `--format table` (default) | Terminal viewing |
| JSON | `--format json` | Automation, CI/CD, `gpuaudit diff` |
| Markdown | `--format markdown` | PRs, wikis, docs |
| Slack | `--format slack` | Slack webhook integration |

## How it works

1. **Discovery** — Scans EC2, SageMaker, EKS node groups, and Kubernetes API across multiple regions for GPU resources
2. **Metrics** — Collects 7-day CloudWatch metrics: CPU, network I/O for EC2; GPU utilization, GPU memory, invocations for SageMaker
3. **K8s allocation** — Lists pods requesting `nvidia.com/gpu` resources and maps them to nodes
4. **Analysis** — Applies 7 waste detection rules with severity levels (critical/warning/info)
5. **Recommendations** — Generates specific actions (terminate, downsize, switch pricing) with estimated monthly savings

## Project structure

```
gpuaudit/
├── cmd/gpuaudit/          CLI entry point (cobra)
├── internal/
│   ├── models/            Core data types (GPUInstance, WasteSignal, Recommendation)
│   ├── pricing/           Bundled GPU pricing database (40+ instance types)
│   ├── analysis/          Waste detection rules engine (7 rules)
│   ├── diff/              Scan comparison logic
│   ├── output/            Formatters (table, JSON, markdown, Slack, diff)
│   └── providers/
│       ├── aws/           EC2, SageMaker, EKS, CloudWatch, Cost Explorer
│       └── k8s/           Kubernetes API GPU node/pod discovery
└── LICENSE                Apache 2.0
```

## Roadmap

- [ ] DCGM GPU metrics via Kubernetes (actual GPU utilization, not just allocation)
- [ ] SageMaker training job analysis
- [ ] Multi-account (AWS Organizations) scanning
- [ ] GCP + Azure support
- [ ] GitHub Action for scheduled scans

## License

Apache 2.0
