# K8s GPU Metrics Collection

## Goal

Collect GPU utilization metrics for Kubernetes GPU nodes discovered by gpuaudit, using a per-node fallback chain of three sources: CloudWatch Container Insights, DCGM exporter scrape, and Prometheus query. Enable utilization-based waste detection for K8s GPU nodes (currently limited to allocation-based detection only).

## Architecture

Three metrics sources, tried in priority order **per node** (stop at the first source that returns data for a given node):

1. **CloudWatch Container Insights** — AWS API call, no in-cluster access needed beyond what we already have.
2. **DCGM exporter scrape** — probe port 9400 on dcgm-exporter pods via K8s API proxy.
3. **Prometheus query** — query a user-configured Prometheus endpoint for historical GPU metrics.

All three populate the same existing fields: `GPUInstance.AvgGPUUtilization` and `GPUInstance.AvgGPUMemUtilization`.

## Data Flow

```
1. AWS scan → ScanResult (EC2, SageMaker, EKS)
2. K8s scan → []GPUInstance (nodes + allocation)
3. Enrich K8s GPU metrics (fallback chain):
   a. CloudWatch Container Insights (if AWS creds available, !skipMetrics)
   b. DCGM scrape via K8s API proxy (for nodes still missing metrics)
   c. Prometheus query (for remaining nodes, if --prom-url or --prom-endpoint set)
4. AnalyzeAll on K8s instances
5. Merge into result
```

Steps 3a through 3c each skip nodes that already have `AvgGPUUtilization` populated by a prior step.

## Source 1: CloudWatch Container Insights

Requires the CloudWatch Observability EKS add-on to be installed in the cluster. If not installed, the query returns empty (not an error) and we fall through.

**Metrics queried:**
- `node_gpu_utilization` (Average) — maps to `AvgGPUUtilization`
- `node_gpu_memory_utilization` (Average) — maps to `AvgGPUMemUtilization`

**Namespace:** `ContainerInsights`

**Dimensions:** `ClusterName` + `InstanceId`

**Implementation:** New function `EnrichK8sGPUMetrics(ctx, client CloudWatchClient, instances []GPUInstance, clusterName string, window MetricWindow)` in `internal/providers/aws/cloudwatch.go`, following the same pattern as `EnrichEC2Metrics` and `EnrichSageMakerMetrics`.

**Prerequisites per node:** The node must have an EC2 instance ID (extracted from `providerID`). Non-AWS nodes are skipped for this source.

**Wiring:** Called from `main.go` after the K8s scan returns instances, passing the CloudWatch client from the AWS config. Only called when AWS credentials are available and `!skipMetrics`.

## Source 2: DCGM Exporter Scrape

Auto-detected, no user configuration needed.

**Discovery:** List pods across all namespaces matching labels `app=nvidia-dcgm-exporter` or `app.kubernetes.io/name=dcgm-exporter`. If no pods found, log `"DCGM exporter not detected, skipping"` and fall through to Prometheus.

**Scraping:** For each GPU node still missing metrics, find the dcgm-exporter pod on that node (match by `pod.Spec.NodeName`), then scrape `/metrics` on port 9400 via the K8s API proxy (`ProxyGet`).

**Metrics parsed:**
- `DCGM_FI_DEV_GPU_UTIL` — maps to `AvgGPUUtilization`
- `DCGM_FI_DEV_MEM_COPY_UTIL` — maps to `AvgGPUMemUtilization`

These are point-in-time values, not historical averages. The analysis rule's confidence (0.85 vs 0.9) accounts for this lower fidelity.

**Prometheus text format parsing:** Use `prometheus/common/expfmt` to parse the scrape response.

**K8s client extension:** Add `ProxyGet(ctx, namespace, podName, port, path string) ([]byte, error)` to the `K8sClient` interface. Wraps `clientset.CoreV1().Pods(ns).ProxyGet()`.

**Stderr output:**
```
  Probing DCGM exporter on GPU nodes...
  DCGM: got GPU metrics for 3 of 5 remaining nodes
```

## Source 3: Prometheus Query

Only attempted when `--prom-url` or `--prom-endpoint` is provided. No auto-discovery.

**CLI flags:**
- `--prom-url` — full URL to a Prometheus-compatible API (e.g., `https://prometheus.corp.example.com`, AMP endpoint, Grafana Cloud). Hit directly via HTTP.
- `--prom-endpoint` — in-cluster service as `namespace/service:port` (e.g., `monitoring/prometheus:9090`). Proxied through the K8s API server.

These flags are mutually exclusive. Error if both are set.

**Query:** Batch all remaining nodes into one PromQL query:
```
avg_over_time(DCGM_FI_DEV_GPU_UTIL{node=~"node1|node2|..."}[7d])
```
And similarly for `DCGM_FI_DEV_MEM_COPY_UTIL`.

**API:** HTTP GET to `/api/v1/query`, parse the standard Prometheus JSON response. No client library — plain `net/http` for direct URLs, K8s API proxy for in-cluster endpoints.

**Stderr output:**
```
  Querying Prometheus at monitoring/prometheus:9090...
  Prometheus: got GPU metrics for 2 of 3 remaining nodes
```

## Analysis Rule

New rule `ruleK8sLowGPUUtil` in `internal/analysis/rules.go`:

- **Source filter:** `SourceK8sNode` only
- **Guard:** `AvgGPUUtilization != nil` (skip nodes where no metrics were collected)
- **Threshold:** average GPU utilization < 10%
- **Signal type:** `low_utilization`
- **Severity:** Critical
- **Confidence:** 0.85
- **Recommendation:** "GPU utilization averaging X%. Consider bin-packing more workloads, downsizing, or removing from the node pool."
- **Savings estimate:** `MonthlyCost * 0.8` (same rough estimate as SageMaker equivalent)

**Interplay with `ruleK8sUnallocatedGPU`:** Both rules can fire on the same node. Unallocated detects zero pod scheduling (allocation-based). Low-util detects pods that are scheduled but barely using the GPU (utilization-based). Different problems, different fixes.

## File Changes

- **Modify:** `internal/providers/aws/cloudwatch.go` — add `EnrichK8sGPUMetrics()`
- **Create:** `internal/providers/k8s/metrics.go` — DCGM scraping, Prometheus querying, fallback orchestration
- **Create:** `internal/providers/k8s/metrics_test.go` — tests for DCGM and Prometheus paths
- **Modify:** `internal/providers/k8s/discover.go` — extend `K8sClient` interface with `ProxyGet` (DCGM pod discovery uses existing `ListPods` with label selector)
- **Modify:** `internal/providers/k8s/scanner.go` — wire metrics enrichment into the K8s scan, accept new options
- **Modify:** `internal/analysis/rules.go` — add `ruleK8sLowGPUUtil`
- **Modify:** `internal/analysis/rules_test.go` — tests for the new rule
- **Modify:** `cmd/gpuaudit/main.go` — add `--prom-url` and `--prom-endpoint` flags, wire CloudWatch enrichment for K8s instances

## Error Handling

- **CloudWatch returns empty:** Not an error. Container Insights add-on probably not installed. Fall through to DCGM.
- **No EC2 instance ID on a node:** Skip CW enrichment for that node (non-AWS or providerID not set).
- **No dcgm-exporter pods found:** Log on stderr, fall through to Prometheus.
- **DCGM scrape fails for a node:** Warn on stderr, continue with other nodes. Don't fail the scan.
- **Prometheus endpoint unreachable:** Warn on stderr, continue without metrics for remaining nodes.
- **Both `--prom-url` and `--prom-endpoint` set:** Return an error at flag validation time.

## New Dependencies

- `prometheus/common/expfmt` — for parsing Prometheus text format from DCGM exporter scrapes. Small, well-established library.

## IAM Policy

No new IAM permissions required. `EnrichK8sGPUMetrics` uses the existing `cloudwatch:GetMetricData` permission already in the IAM policy output.

## RBAC

The K8s API proxy calls (`ProxyGet` to pods) require the `pods/proxy` resource permission. For DCGM scraping:
```
- apiGroups: [""]
  resources: ["pods/proxy"]
  verbs: ["get"]
```
This should be documented and added to any RBAC guide.
