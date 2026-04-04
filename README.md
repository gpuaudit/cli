# gpuaudit

Scan your AWS account for GPU waste and get actionable recommendations to cut your cloud spend.

```
$ gpuaudit scan --profile ml-prod

  GPU Fleet Summary
  Total GPU instances:       14
  Total monthly GPU spend:   $47,832
  Estimated monthly waste:   $18,240  (38%)

  CRITICAL (3 instances, $8,940/mo potential savings)

  i-0a1b2c3d4e  g5.12xlarge (4x A10G)     $4,380/mo   Idle — no activity for 18 days → terminate
  i-9f8e7d6c5b  p4d.24xlarge (8x A100)    $23,652/mo   Idle — <1% CPU for 6 days → terminate
  sagemaker:asr ml.g6.48xlarge (8x L40S)   $9,490/mo   GPU util avg 8% → downsize to ml.g5.xlarge
```

## What it detects

- **Idle GPU instances** — running but doing nothing (low CPU + near-zero network for 24+ hours)
- **Oversized GPU** — multi-GPU instances where utilization suggests a single GPU would suffice
- **Pricing mismatch** — on-demand instances running 30+ days that should be Reserved Instances
- **Stale instances** — non-production instances running 90+ days
- **SageMaker low utilization** — endpoints with <10% GPU utilization
- **SageMaker oversized** — endpoints using <30% GPU memory on multi-GPU instances

## Install

```bash
go install github.com/maksimov/gpuaudit/cmd/gpuaudit@latest
```

Or build from source:

```bash
git clone https://github.com/maksimov/gpuaudit.git
cd gpuaudit
go build -o gpuaudit ./cmd/gpuaudit
```

## Quick start

```bash
# Uses default AWS credentials (~/.aws/credentials or environment variables)
gpuaudit scan

# Specific profile and region
gpuaudit scan --profile production --region us-east-1

# JSON output for automation
gpuaudit scan --format json --output report.json

# Markdown for docs/PRs
gpuaudit scan --format markdown

# Slack Block Kit payload (pipe to webhook)
gpuaudit scan --format slack --output - | curl -X POST -H 'Content-Type: application/json' -d @- $SLACK_WEBHOOK

# Skip CloudWatch metrics (faster, less accurate)
gpuaudit scan --skip-metrics

# Skip SageMaker scanning
gpuaudit scan --skip-sagemaker
```

## IAM permissions

gpuaudit is read-only. It never modifies your infrastructure. Generate the minimal IAM policy:

```bash
gpuaudit iam-policy
```

This outputs a JSON policy requiring only `Describe*`, `List*`, `Get*` permissions for EC2, SageMaker, CloudWatch, Cost Explorer, and Pricing APIs.

## GPU pricing reference

```bash
# List all GPU instance pricing
gpuaudit pricing

# Filter by GPU model
gpuaudit pricing --gpu H100
gpuaudit pricing --gpu A10G
gpuaudit pricing --gpu T4
```

## Output formats

| Format | Flag | Use case |
|---|---|---|
| Table | `--format table` (default) | Terminal viewing |
| JSON | `--format json` | Automation, CI/CD pipelines |
| Markdown | `--format markdown` | PRs, wikis, docs |
| Slack | `--format slack` | Slack webhook integration |

## How it works

1. **Discovery** — Scans EC2 and SageMaker across multiple regions for GPU instance families (g4dn, g5, g6, g6e, p4d, p4de, p5, inf2, trn1)
2. **Metrics** — Collects 7-day CloudWatch metrics: CPU, network I/O for EC2; GPU utilization, GPU memory, invocations for SageMaker
3. **Analysis** — Applies 6 waste detection rules with severity levels (critical/warning)
4. **Recommendations** — Generates specific actions (terminate, downsize, switch pricing) with estimated monthly savings

Regions scanned by default: us-east-1, us-east-2, us-west-2, eu-west-1, eu-west-2, eu-central-1, ap-southeast-1, ap-northeast-1, ap-south-1.

## Project structure

```
gpuaudit/
├── cmd/gpuaudit/          CLI entry point (cobra)
├── internal/
│   ├── models/            Core data types (GPUInstance, WasteSignal, Recommendation)
│   ├── pricing/           Bundled GPU pricing database (40+ instance types)
│   ├── analysis/          Waste detection rules engine
│   ├── output/            Formatters (table, JSON, markdown, Slack)
│   └── providers/aws/     EC2, SageMaker, CloudWatch, scanner orchestrator
├── ARCHITECTURE.md        Detailed technical design
└── LICENSE                Apache 2.0
```

## Roadmap

- [ ] AWS Cost Explorer integration (actual vs projected spend)
- [ ] EKS GPU pod discovery
- [ ] SageMaker training job analysis
- [ ] Multi-account (AWS Organizations) scanning
- [ ] GCP + Azure support
- [ ] GitHub Action for scheduled scans
- [ ] Historical scan comparison (`gpuaudit diff`)

## License

Apache 2.0
