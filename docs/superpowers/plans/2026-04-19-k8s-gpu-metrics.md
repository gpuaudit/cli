# K8s GPU Metrics Collection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Collect GPU utilization metrics for Kubernetes GPU nodes via a per-node fallback chain (CloudWatch Container Insights → DCGM exporter → Prometheus), and add a utilization-based waste detection rule.

**Architecture:** Three metrics sources tried in priority order per node, all populating the existing `AvgGPUUtilization` and `AvgGPUMemUtilization` fields on `GPUInstance`. A new analysis rule `ruleK8sLowGPUUtil` flags nodes with GPU utilization < 10%. The fallback chain is wired in `main.go` between K8s discovery and analysis.

**Tech Stack:** Go, AWS SDK v2 (CloudWatch), client-go (K8s API proxy), prometheus/common/expfmt (Prometheus text parsing), net/http (Prometheus API)

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/providers/aws/cloudwatch.go` | Add `EnrichK8sGPUMetrics()` — CloudWatch Container Insights queries |
| `internal/providers/aws/cloudwatch_test.go` | Tests for `EnrichK8sGPUMetrics()` (new file) |
| `internal/providers/k8s/discover.go` | Extend `K8sClient` interface with `ProxyGet` |
| `internal/providers/k8s/scanner.go` | Extend `ScanOptions` with Prometheus config, export `BuildClientPublic` |
| `internal/providers/k8s/metrics.go` | DCGM scraping, Prometheus querying, fallback orchestration (new file) |
| `internal/providers/k8s/metrics_test.go` | Tests for DCGM and Prometheus paths (new file) |
| `internal/analysis/rules.go` | Add `ruleK8sLowGPUUtil` |
| `internal/analysis/rules_test.go` | Tests for new rule |
| `cmd/gpuaudit/main.go` | Add `--prom-url`, `--prom-endpoint` flags; wire CW enrichment for K8s instances |

---

### Task 1: CloudWatch Container Insights Enrichment

**Files:**
- Create: `internal/providers/aws/cloudwatch_test.go`
- Modify: `internal/providers/aws/cloudwatch.go:60-80`

This task adds `EnrichK8sGPUMetrics()` following the exact same pattern as the existing `EnrichEC2Metrics()` and `EnrichSageMakerMetrics()` functions. It queries the `ContainerInsights` namespace for `node_gpu_utilization` and `node_gpu_memory_utilization`.

- [ ] **Step 1: Write the failing tests**

Create `internal/providers/aws/cloudwatch_test.go`:

```go
// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/gpuaudit/cli/internal/models"
)

type mockCloudWatchClient struct {
	output *cloudwatch.GetMetricDataOutput
	err    error
}

func (m *mockCloudWatchClient) GetMetricData(ctx context.Context, params *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.output, nil
}

func TestEnrichK8sGPUMetrics_PopulatesUtilization(t *testing.T) {
	client := &mockCloudWatchClient{
		output: &cloudwatch.GetMetricDataOutput{
			MetricDataResults: []cwtypes.MetricDataResult{
				{Id: aws.String("gpu_util_i_abc123"), Values: []float64{45.0, 50.0, 55.0}},
				{Id: aws.String("gpu_mem_i_abc123"), Values: []float64{30.0, 35.0, 40.0}},
			},
		},
	}
	instances := []models.GPUInstance{
		{
			InstanceID: "i-abc123",
			Source:     models.SourceK8sNode,
		},
	}

	EnrichK8sGPUMetrics(context.Background(), client, instances, "ml-cluster", DefaultMetricWindow)

	if instances[0].AvgGPUUtilization == nil {
		t.Fatal("expected GPU utilization to be populated")
	}
	if *instances[0].AvgGPUUtilization != 50.0 {
		t.Errorf("expected avg GPU util 50.0, got %f", *instances[0].AvgGPUUtilization)
	}
	if instances[0].AvgGPUMemUtilization == nil {
		t.Fatal("expected GPU memory utilization to be populated")
	}
	if *instances[0].AvgGPUMemUtilization != 35.0 {
		t.Errorf("expected avg GPU mem util 35.0, got %f", *instances[0].AvgGPUMemUtilization)
	}
}

func TestEnrichK8sGPUMetrics_SkipsNonK8sNodes(t *testing.T) {
	client := &mockCloudWatchClient{
		output: &cloudwatch.GetMetricDataOutput{},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-ec2", Source: models.SourceEC2},
	}

	EnrichK8sGPUMetrics(context.Background(), client, instances, "cluster", DefaultMetricWindow)

	if instances[0].AvgGPUUtilization != nil {
		t.Error("expected nil GPU util for non-K8s instance")
	}
}

func TestEnrichK8sGPUMetrics_SkipsNodesWithoutInstanceID(t *testing.T) {
	client := &mockCloudWatchClient{
		output: &cloudwatch.GetMetricDataOutput{},
	}
	instances := []models.GPUInstance{
		{InstanceID: "node-hostname", Source: models.SourceK8sNode},
	}

	EnrichK8sGPUMetrics(context.Background(), client, instances, "cluster", DefaultMetricWindow)

	if instances[0].AvgGPUUtilization != nil {
		t.Error("expected nil GPU util for node without EC2 instance ID")
	}
}

func TestEnrichK8sGPUMetrics_SkipsAlreadyEnriched(t *testing.T) {
	gpuUtil := 75.0
	client := &mockCloudWatchClient{
		output: &cloudwatch.GetMetricDataOutput{},
	}
	instances := []models.GPUInstance{
		{
			InstanceID:        "i-abc123",
			Source:            models.SourceK8sNode,
			AvgGPUUtilization: &gpuUtil,
		},
	}

	EnrichK8sGPUMetrics(context.Background(), client, instances, "cluster", DefaultMetricWindow)

	if *instances[0].AvgGPUUtilization != 75.0 {
		t.Errorf("expected existing value 75.0 to be preserved, got %f", *instances[0].AvgGPUUtilization)
	}
}

func TestEnrichK8sGPUMetrics_HandlesAPIError(t *testing.T) {
	client := &mockCloudWatchClient{
		err: fmt.Errorf("access denied"),
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-abc123", Source: models.SourceK8sNode},
	}

	EnrichK8sGPUMetrics(context.Background(), client, instances, "cluster", DefaultMetricWindow)

	if instances[0].AvgGPUUtilization != nil {
		t.Error("expected nil GPU util after API error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/aws/ -run TestEnrichK8sGPUMetrics -v`
Expected: FAIL — `EnrichK8sGPUMetrics` not defined

- [ ] **Step 3: Implement EnrichK8sGPUMetrics**

Add to `internal/providers/aws/cloudwatch.go`, after the `EnrichSageMakerMetrics` function (after line 80):

```go
// EnrichK8sGPUMetrics populates GPU utilization metrics on K8s nodes using CloudWatch Container Insights.
func EnrichK8sGPUMetrics(ctx context.Context, client CloudWatchClient, instances []models.GPUInstance, clusterName string, window MetricWindow) {
	type nodeRef struct {
		index      int
		instanceID string
	}
	var nodes []nodeRef
	for i := range instances {
		inst := &instances[i]
		if inst.Source != models.SourceK8sNode {
			continue
		}
		if inst.AvgGPUUtilization != nil {
			continue
		}
		if !strings.HasPrefix(inst.InstanceID, "i-") {
			continue
		}
		nodes = append(nodes, nodeRef{index: i, instanceID: inst.InstanceID})
	}
	if len(nodes) == 0 {
		return
	}

	now := time.Now()
	start := now.Add(-window.Duration)

	clusterDim := cwtypes.Dimension{
		Name:  aws.String("ClusterName"),
		Value: aws.String(clusterName),
	}

	for _, node := range nodes {
		instanceDim := cwtypes.Dimension{
			Name:  aws.String("InstanceId"),
			Value: aws.String(node.instanceID),
		}

		safeID := strings.ReplaceAll(node.instanceID, "-", "_")

		queries := []cwtypes.MetricDataQuery{
			metricQuery2("gpu_util_"+safeID, "ContainerInsights", "node_gpu_utilization", "Average", window.Period, clusterDim, instanceDim),
			metricQuery2("gpu_mem_"+safeID, "ContainerInsights", "node_gpu_memory_utilization", "Average", window.Period, clusterDim, instanceDim),
		}

		results, err := fetchMetrics(ctx, client, queries, start, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: Container Insights metrics unavailable for %s: %v\n", node.instanceID, err)
			continue
		}

		instances[node.index].AvgGPUUtilization = results["gpu_util_"+safeID]
		instances[node.index].AvgGPUMemUtilization = results["gpu_mem_"+safeID]
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/aws/ -run TestEnrichK8sGPUMetrics -v`
Expected: PASS (all 5 tests)

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/providers/aws/cloudwatch.go internal/providers/aws/cloudwatch_test.go
git commit -m "Add EnrichK8sGPUMetrics for CloudWatch Container Insights GPU metrics"
```

---

### Task 2: Extend K8sClient Interface with ProxyGet

**Files:**
- Modify: `internal/providers/k8s/discover.go:24-27`
- Modify: `internal/providers/k8s/scanner.go:91-101`
- Modify: `internal/providers/k8s/discover_test.go:19-30`

This task adds `ProxyGet` to the `K8sClient` interface and updates the mock and wrapper. This is needed for both DCGM scraping (Task 3) and Prometheus in-cluster queries (Task 4).

- [ ] **Step 1: Add ProxyGet to the K8sClient interface**

In `internal/providers/k8s/discover.go`, change the `K8sClient` interface (lines 24-27) from:

```go
type K8sClient interface {
	ListNodes(ctx context.Context, opts metav1.ListOptions) (*corev1.NodeList, error)
	ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error)
}
```

to:

```go
type K8sClient interface {
	ListNodes(ctx context.Context, opts metav1.ListOptions) (*corev1.NodeList, error)
	ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error)
	ProxyGet(ctx context.Context, namespace, podName, port, path string) ([]byte, error)
}
```

- [ ] **Step 2: Implement ProxyGet on k8sClientWrapper**

In `internal/providers/k8s/scanner.go`, add this method after the `ListPods` method (after line 101):

```go
func (w *k8sClientWrapper) ProxyGet(ctx context.Context, namespace, podName, port, path string) ([]byte, error) {
	return w.clientset.CoreV1().Pods(namespace).ProxyGet("http", podName, port, path, nil).DoRaw(ctx)
}
```

- [ ] **Step 3: Add ProxyGet to the mock in tests**

In `internal/providers/k8s/discover_test.go`, change the `mockK8sClient` struct (lines 19-22) from:

```go
type mockK8sClient struct {
	nodes *corev1.NodeList
	pods  *corev1.PodList
}
```

to:

```go
type mockK8sClient struct {
	nodes     *corev1.NodeList
	pods      *corev1.PodList
	proxyData map[string][]byte
	proxyErr  error
}
```

And add the method after `ListPods` (after line 30):

```go
func (m *mockK8sClient) ProxyGet(ctx context.Context, namespace, podName, port, path string) ([]byte, error) {
	if m.proxyErr != nil {
		return nil, m.proxyErr
	}
	key := fmt.Sprintf("%s/%s:%s%s", namespace, podName, port, path)
	if data, ok := m.proxyData[key]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("no mock data for %s", key)
}
```

- [ ] **Step 4: Run tests to verify nothing is broken**

Run: `go test ./internal/providers/k8s/ -v`
Expected: All existing tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/providers/k8s/discover.go internal/providers/k8s/scanner.go internal/providers/k8s/discover_test.go
git commit -m "Add ProxyGet to K8sClient interface for pod API proxy"
```

---

### Task 3: DCGM Exporter Scraping

**Files:**
- Create: `internal/providers/k8s/metrics.go`
- Create: `internal/providers/k8s/metrics_test.go`

This task implements DCGM exporter auto-discovery and metric scraping. It discovers dcgm-exporter pods by label, matches them to GPU nodes, scrapes `/metrics` on port 9400, and parses `DCGM_FI_DEV_GPU_UTIL` and `DCGM_FI_DEV_MEM_COPY_UTIL`.

- [ ] **Step 1: Add the `prometheus/common` dependency**

Run: `go get github.com/prometheus/common@latest`

This will also pull in `github.com/prometheus/client_model` (needed for `dto.MetricFamily`).

- [ ] **Step 2: Write the failing tests**

Create `internal/providers/k8s/metrics_test.go`:

```go
// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gpuaudit/cli/internal/models"
)

func dcgmPod(name, namespace, nodeName string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "dcgm-exporter",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}

const sampleDCGMMetrics = `# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization.
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-abc",device="nvidia0",modelName="NVIDIA A10G",Hostname="node1"} 42.0
DCGM_FI_DEV_GPU_UTIL{gpu="1",UUID="GPU-def",device="nvidia1",modelName="NVIDIA A10G",Hostname="node1"} 38.0
# HELP DCGM_FI_DEV_MEM_COPY_UTIL GPU memory utilization.
# TYPE DCGM_FI_DEV_MEM_COPY_UTIL gauge
DCGM_FI_DEV_MEM_COPY_UTIL{gpu="0",UUID="GPU-abc",device="nvidia0",modelName="NVIDIA A10G",Hostname="node1"} 55.0
DCGM_FI_DEV_MEM_COPY_UTIL{gpu="1",UUID="GPU-def",device="nvidia1",modelName="NVIDIA A10G",Hostname="node1"} 60.0
`

func TestEnrichDCGMMetrics_PopulatesUtilization(t *testing.T) {
	client := &mockK8sClient{
		nodes: &corev1.NodeList{},
		pods: &corev1.PodList{
			Items: []corev1.Pod{
				dcgmPod("dcgm-exporter-abc", "gpu-operator", "i-node1"),
			},
		},
		proxyData: map[string][]byte{
			"gpu-operator/dcgm-exporter-abc:9400/metrics": []byte(sampleDCGMMetrics),
		},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode, Name: "cluster/i-node1"},
	}

	enriched := EnrichDCGMMetrics(context.Background(), client, instances)

	if instances[0].AvgGPUUtilization == nil {
		t.Fatal("expected GPU utilization to be populated")
	}
	if *instances[0].AvgGPUUtilization != 40.0 {
		t.Errorf("expected avg GPU util 40.0 (average of 42 and 38), got %f", *instances[0].AvgGPUUtilization)
	}
	if instances[0].AvgGPUMemUtilization == nil {
		t.Fatal("expected GPU memory utilization to be populated")
	}
	if *instances[0].AvgGPUMemUtilization != 57.5 {
		t.Errorf("expected avg GPU mem util 57.5 (average of 55 and 60), got %f", *instances[0].AvgGPUMemUtilization)
	}
	if enriched != 1 {
		t.Errorf("expected 1 enriched node, got %d", enriched)
	}
}

func TestEnrichDCGMMetrics_SkipsAlreadyEnriched(t *testing.T) {
	gpuUtil := 75.0
	client := &mockK8sClient{
		nodes: &corev1.NodeList{},
		pods: &corev1.PodList{
			Items: []corev1.Pod{
				dcgmPod("dcgm-exporter-abc", "gpu-operator", "i-node1"),
			},
		},
		proxyData: map[string][]byte{
			"gpu-operator/dcgm-exporter-abc:9400/metrics": []byte(sampleDCGMMetrics),
		},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode, AvgGPUUtilization: &gpuUtil},
	}

	enriched := EnrichDCGMMetrics(context.Background(), client, instances)

	if *instances[0].AvgGPUUtilization != 75.0 {
		t.Error("should not overwrite existing utilization")
	}
	if enriched != 0 {
		t.Errorf("expected 0 enriched nodes, got %d", enriched)
	}
}

func TestEnrichDCGMMetrics_NoDCGMPods(t *testing.T) {
	client := &mockK8sClient{
		nodes: &corev1.NodeList{},
		pods:  &corev1.PodList{Items: []corev1.Pod{}},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode},
	}

	enriched := EnrichDCGMMetrics(context.Background(), client, instances)

	if instances[0].AvgGPUUtilization != nil {
		t.Error("expected nil when no DCGM pods")
	}
	if enriched != 0 {
		t.Errorf("expected 0 enriched, got %d", enriched)
	}
}

func TestEnrichDCGMMetrics_HandlesScrapeError(t *testing.T) {
	client := &mockK8sClient{
		nodes: &corev1.NodeList{},
		pods: &corev1.PodList{
			Items: []corev1.Pod{
				dcgmPod("dcgm-exporter-abc", "gpu-operator", "i-node1"),
			},
		},
		proxyErr: fmt.Errorf("connection refused"),
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode},
	}

	enriched := EnrichDCGMMetrics(context.Background(), client, instances)

	if instances[0].AvgGPUUtilization != nil {
		t.Error("expected nil after scrape error")
	}
	if enriched != 0 {
		t.Errorf("expected 0 enriched, got %d", enriched)
	}
}

func TestParseDCGMMetrics(t *testing.T) {
	gpuUtil, memUtil := parseDCGMMetrics([]byte(sampleDCGMMetrics))

	if gpuUtil == nil {
		t.Fatal("expected gpu util")
	}
	if *gpuUtil != 40.0 {
		t.Errorf("expected 40.0, got %f", *gpuUtil)
	}
	if memUtil == nil {
		t.Fatal("expected mem util")
	}
	if *memUtil != 57.5 {
		t.Errorf("expected 57.5, got %f", *memUtil)
	}
}

func TestParseDCGMMetrics_EmptyInput(t *testing.T) {
	gpuUtil, memUtil := parseDCGMMetrics([]byte(""))
	if gpuUtil != nil || memUtil != nil {
		t.Error("expected nil for empty input")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/providers/k8s/ -run "TestEnrichDCGM|TestParseDCGM" -v`
Expected: FAIL — functions not defined

- [ ] **Step 4: Implement DCGM metrics enrichment**

Create `internal/providers/k8s/metrics.go`:

```go
// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"context"
	"fmt"
	"os"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gpuaudit/cli/internal/models"
)

// EnrichDCGMMetrics discovers dcgm-exporter pods and scrapes GPU metrics for K8s nodes
// that don't already have AvgGPUUtilization populated. Returns the number of nodes enriched.
func EnrichDCGMMetrics(ctx context.Context, client K8sClient, instances []models.GPUInstance) int {
	needsMetrics := make(map[string]int)
	for i := range instances {
		inst := &instances[i]
		if inst.Source != models.SourceK8sNode || inst.AvgGPUUtilization != nil {
			continue
		}
		needsMetrics[inst.InstanceID] = i
	}
	if len(needsMetrics) == 0 {
		return 0
	}

	dcgmPods, err := findDCGMPods(ctx, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not list DCGM exporter pods: %v\n", err)
		return 0
	}
	if len(dcgmPods) == 0 {
		fmt.Fprintf(os.Stderr, "  DCGM exporter not detected, skipping\n")
		return 0
	}

	fmt.Fprintf(os.Stderr, "  Probing DCGM exporter on GPU nodes...\n")

	enriched := 0
	for _, pod := range dcgmPods {
		idx, ok := needsMetrics[pod.Spec.NodeName]
		if !ok {
			continue
		}

		data, err := client.ProxyGet(ctx, pod.Namespace, pod.Name, "9400", "/metrics")
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: DCGM scrape failed for %s: %v\n", pod.Spec.NodeName, err)
			continue
		}

		gpuUtil, memUtil := parseDCGMMetrics(data)
		if gpuUtil != nil {
			instances[idx].AvgGPUUtilization = gpuUtil
			instances[idx].AvgGPUMemUtilization = memUtil
			enriched++
		}
	}

	fmt.Fprintf(os.Stderr, "  DCGM: got GPU metrics for %d of %d remaining nodes\n", enriched, len(needsMetrics))
	return enriched
}

func findDCGMPods(ctx context.Context, client K8sClient) ([]corev1.Pod, error) {
	podList, err := client.ListPods(ctx, "", metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=dcgm-exporter",
	})
	if err != nil {
		return nil, err
	}
	if len(podList.Items) > 0 {
		return runningPods(podList.Items), nil
	}

	podList, err = client.ListPods(ctx, "", metav1.ListOptions{
		LabelSelector: "app=nvidia-dcgm-exporter",
	})
	if err != nil {
		return nil, err
	}
	return runningPods(podList.Items), nil
}

func runningPods(pods []corev1.Pod) []corev1.Pod {
	var result []corev1.Pod
	for _, p := range pods {
		if p.Status.Phase == corev1.PodRunning {
			result = append(result, p)
		}
	}
	return result
}

func parseDCGMMetrics(data []byte) (gpuUtil, memUtil *float64) {
	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(bytes.NewReader(data))
	if err != nil {
		return nil, nil
	}

	gpuUtil = avgMetricValue(families["DCGM_FI_DEV_GPU_UTIL"])
	memUtil = avgMetricValue(families["DCGM_FI_DEV_MEM_COPY_UTIL"])
	return gpuUtil, memUtil
}

func avgMetricValue(family *dto.MetricFamily) *float64 {
	if family == nil || len(family.Metric) == 0 {
		return nil
	}
	sum := 0.0
	count := 0
	for _, m := range family.Metric {
		if m.Gauge != nil && m.Gauge.Value != nil {
			sum += *m.Gauge.Value
			count++
		}
	}
	if count == 0 {
		return nil
	}
	avg := sum / float64(count)
	return &avg
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/providers/k8s/ -run "TestEnrichDCGM|TestParseDCGM" -v`
Expected: PASS (all 6 tests)

- [ ] **Step 6: Run full test suite**

Run: `go test ./...`
Expected: All tests pass

- [ ] **Step 7: Commit**

```bash
git add internal/providers/k8s/metrics.go internal/providers/k8s/metrics_test.go go.mod go.sum
git commit -m "Add DCGM exporter scraping for K8s GPU metrics"
```

---

### Task 4: Prometheus Query Enrichment

**Files:**
- Modify: `internal/providers/k8s/metrics.go`
- Modify: `internal/providers/k8s/metrics_test.go`

This task adds the Prometheus query path — the third fallback. It supports both direct URL (`--prom-url`) and in-cluster service endpoint (`--prom-endpoint`), querying `avg_over_time(DCGM_FI_DEV_GPU_UTIL{node=~"..."}[7d])`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/providers/k8s/metrics_test.go`:

```go
import (
	"net/http"
	"net/http/httptest"
	"strings"
)
```

Add these test functions:

```go
func TestEnrichPrometheusMetrics_PopulatesFromDirectURL(t *testing.T) {
	promResponse := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{"metric": {"node": "i-node1"}, "value": [1700000000, "65.5"]},
				{"metric": {"node": "i-node2"}, "value": [1700000000, "30.0"]}
			]
		}
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		query := r.URL.Query().Get("query")
		if !strings.Contains(query, "DCGM_FI_DEV_GPU_UTIL") {
			t.Errorf("unexpected query: %s", query)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(promResponse))
	}))
	defer server.Close()

	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode, Name: "cluster/i-node1"},
		{InstanceID: "i-node2", Source: models.SourceK8sNode, Name: "cluster/i-node2"},
	}
	opts := PrometheusOptions{URL: server.URL}

	enriched := EnrichPrometheusMetrics(context.Background(), nil, instances, opts)

	if enriched != 2 {
		t.Errorf("expected 2 enriched, got %d", enriched)
	}
	if instances[0].AvgGPUUtilization == nil || *instances[0].AvgGPUUtilization != 65.5 {
		t.Errorf("expected node1 GPU util 65.5, got %v", instances[0].AvgGPUUtilization)
	}
	if instances[1].AvgGPUUtilization == nil || *instances[1].AvgGPUUtilization != 30.0 {
		t.Errorf("expected node2 GPU util 30.0, got %v", instances[1].AvgGPUUtilization)
	}
}

func TestEnrichPrometheusMetrics_SkipsAlreadyEnriched(t *testing.T) {
	gpuUtil := 80.0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode, AvgGPUUtilization: &gpuUtil},
	}
	opts := PrometheusOptions{URL: server.URL}

	enriched := EnrichPrometheusMetrics(context.Background(), nil, instances, opts)

	if enriched != 0 {
		t.Errorf("expected 0 enriched, got %d", enriched)
	}
}

func TestEnrichPrometheusMetrics_NoOptions(t *testing.T) {
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode},
	}

	enriched := EnrichPrometheusMetrics(context.Background(), nil, instances, PrometheusOptions{})

	if enriched != 0 {
		t.Errorf("expected 0 enriched, got %d", enriched)
	}
}

func TestEnrichPrometheusMetrics_InClusterEndpoint(t *testing.T) {
	promResponse := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{"metric": {"node": "i-node1"}, "value": [1700000000, "50.0"]}
			]
		}
	}`
	client := &mockK8sClient{
		nodes: &corev1.NodeList{},
		pods:  &corev1.PodList{},
		proxyData: map[string][]byte{
			"monitoring/prometheus:9090/api/v1/query": []byte(promResponse),
		},
	}
	instances := []models.GPUInstance{
		{InstanceID: "i-node1", Source: models.SourceK8sNode},
	}
	opts := PrometheusOptions{Endpoint: "monitoring/prometheus:9090"}

	enriched := EnrichPrometheusMetrics(context.Background(), client, instances, opts)

	if enriched != 1 {
		t.Errorf("expected 1 enriched, got %d", enriched)
	}
	if instances[0].AvgGPUUtilization == nil || *instances[0].AvgGPUUtilization != 50.0 {
		t.Errorf("expected 50.0, got %v", instances[0].AvgGPUUtilization)
	}
}

func TestParsePrometheusEndpoint(t *testing.T) {
	tests := []struct {
		input     string
		namespace string
		service   string
		port      string
		wantErr   bool
	}{
		{"monitoring/prometheus:9090", "monitoring", "prometheus", "9090", false},
		{"kube-system/thanos-query:10902", "kube-system", "thanos-query", "10902", false},
		{"invalid", "", "", "", true},
		{"ns/svc", "", "", "", true},
	}
	for _, tt := range tests {
		ns, svc, port, err := parsePrometheusEndpoint(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parsePrometheusEndpoint(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			continue
		}
		if ns != tt.namespace || svc != tt.service || port != tt.port {
			t.Errorf("parsePrometheusEndpoint(%q) = (%q,%q,%q), want (%q,%q,%q)",
				tt.input, ns, svc, port, tt.namespace, tt.service, tt.port)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/k8s/ -run "TestEnrichPrometheus|TestParsePrometheus" -v`
Expected: FAIL — functions not defined

- [ ] **Step 3: Implement Prometheus metrics enrichment**

Add to `internal/providers/k8s/metrics.go` (additional imports at the top):

```go
import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)
```

Add these types and functions:

```go
// PrometheusOptions configures how to reach a Prometheus-compatible API.
type PrometheusOptions struct {
	URL      string
	Endpoint string
}

// EnrichPrometheusMetrics queries a Prometheus endpoint for GPU utilization metrics
// for K8s nodes that don't already have AvgGPUUtilization populated.
func EnrichPrometheusMetrics(ctx context.Context, client K8sClient, instances []models.GPUInstance, opts PrometheusOptions) int {
	if opts.URL == "" && opts.Endpoint == "" {
		return 0
	}

	type nodeRef struct {
		index int
		name  string
	}
	var nodes []nodeRef
	for i := range instances {
		inst := &instances[i]
		if inst.Source != models.SourceK8sNode || inst.AvgGPUUtilization != nil {
			continue
		}
		nodes = append(nodes, nodeRef{index: i, name: inst.InstanceID})
	}
	if len(nodes) == 0 {
		return 0
	}

	source := opts.URL
	if source == "" {
		source = opts.Endpoint
	}
	fmt.Fprintf(os.Stderr, "  Querying Prometheus at %s...\n", source)

	nodeNames := make([]string, len(nodes))
	for i, n := range nodes {
		nodeNames[i] = n.name
	}
	nodeRegex := strings.Join(nodeNames, "|")

	gpuResults := queryPrometheus(ctx, client, opts,
		fmt.Sprintf(`avg_over_time(DCGM_FI_DEV_GPU_UTIL{node=~"%s"}[7d])`, nodeRegex))
	memResults := queryPrometheus(ctx, client, opts,
		fmt.Sprintf(`avg_over_time(DCGM_FI_DEV_MEM_COPY_UTIL{node=~"%s"}[7d])`, nodeRegex))

	enriched := 0
	for _, node := range nodes {
		if val, ok := gpuResults[node.name]; ok {
			instances[node.index].AvgGPUUtilization = &val
			if memVal, ok := memResults[node.name]; ok {
				instances[node.index].AvgGPUMemUtilization = &memVal
			}
			enriched++
		}
	}

	fmt.Fprintf(os.Stderr, "  Prometheus: got GPU metrics for %d of %d remaining nodes\n", enriched, len(nodes))
	return enriched
}

func queryPrometheus(ctx context.Context, client K8sClient, opts PrometheusOptions, query string) map[string]float64 {
	var data []byte
	var err error

	if opts.URL != "" {
		data, err = queryPrometheusHTTP(ctx, opts.URL, query)
	} else {
		data, err = queryPrometheusProxy(ctx, client, opts.Endpoint, query)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: Prometheus query failed: %v\n", err)
		return nil
	}

	return parsePrometheusResponse(data)
}

func queryPrometheusHTTP(ctx context.Context, baseURL, query string) ([]byte, error) {
	u := fmt.Sprintf("%s/api/v1/query?query=%s", strings.TrimRight(baseURL, "/"), url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func queryPrometheusProxy(ctx context.Context, client K8sClient, endpoint, query string) ([]byte, error) {
	ns, svc, port, err := parsePrometheusEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/v1/query?query=%s", url.QueryEscape(query))
	return client.ProxyGet(ctx, ns, svc, port, path)
}

func parsePrometheusEndpoint(endpoint string) (namespace, service, port string, err error) {
	slashIdx := strings.Index(endpoint, "/")
	if slashIdx < 1 {
		return "", "", "", fmt.Errorf("invalid endpoint format %q, expected namespace/service:port", endpoint)
	}
	namespace = endpoint[:slashIdx]
	rest := endpoint[slashIdx+1:]
	colonIdx := strings.LastIndex(rest, ":")
	if colonIdx < 1 {
		return "", "", "", fmt.Errorf("invalid endpoint format %q, expected namespace/service:port", endpoint)
	}
	service = rest[:colonIdx]
	port = rest[colonIdx+1:]
	return namespace, service, port, nil
}

func parsePrometheusResponse(data []byte) map[string]float64 {
	var resp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil
	}
	if resp.Status != "success" {
		return nil
	}

	results := make(map[string]float64)
	for _, r := range resp.Data.Result {
		node := r.Metric["node"]
		if node == "" || len(r.Value) < 2 {
			continue
		}
		var valStr string
		if err := json.Unmarshal(r.Value[1], &valStr); err != nil {
			continue
		}
		val, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			continue
		}
		results[node] = val
	}
	return results
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/k8s/ -run "TestEnrichPrometheus|TestParsePrometheus" -v`
Expected: PASS (all 5 tests)

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/providers/k8s/metrics.go internal/providers/k8s/metrics_test.go
git commit -m "Add Prometheus query enrichment for K8s GPU metrics"
```

---

### Task 5: K8s Low GPU Utilization Analysis Rule

**Files:**
- Modify: `internal/analysis/rules.go`
- Modify: `internal/analysis/rules_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/analysis/rules_test.go`:

```go
func TestRuleK8sLowGPUUtil_FlagsLowUtilization(t *testing.T) {
	inst := models.GPUInstance{
		InstanceID:        "i-node1",
		Source:            models.SourceK8sNode,
		State:             "ready",
		InstanceType:      "g5.xlarge",
		GPUModel:          "A10G",
		GPUCount:          1,
		GPUAllocated:      1,
		MonthlyCost:       734,
		AvgGPUUtilization: ptr(3.5),
	}

	ruleK8sLowGPUUtil(&inst)

	if len(inst.WasteSignals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(inst.WasteSignals))
	}
	if inst.WasteSignals[0].Type != "low_utilization" {
		t.Errorf("expected low_utilization, got %s", inst.WasteSignals[0].Type)
	}
	if inst.WasteSignals[0].Severity != models.SeverityCritical {
		t.Errorf("expected critical, got %s", inst.WasteSignals[0].Severity)
	}
	if inst.WasteSignals[0].Confidence != 0.85 {
		t.Errorf("expected confidence 0.85, got %f", inst.WasteSignals[0].Confidence)
	}
	if len(inst.Recommendations) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(inst.Recommendations))
	}
	if inst.Recommendations[0].MonthlySavings != 734*0.8 {
		t.Errorf("expected savings %.0f, got %f", 734*0.8, inst.Recommendations[0].MonthlySavings)
	}
}

func TestRuleK8sLowGPUUtil_SkipsNonK8s(t *testing.T) {
	inst := models.GPUInstance{
		Source:            models.SourceEC2,
		AvgGPUUtilization: ptr(3.5),
	}

	ruleK8sLowGPUUtil(&inst)

	if len(inst.WasteSignals) != 0 {
		t.Errorf("expected no signals for EC2 instance")
	}
}

func TestRuleK8sLowGPUUtil_SkipsNoMetrics(t *testing.T) {
	inst := models.GPUInstance{
		Source: models.SourceK8sNode,
		State:  "ready",
	}

	ruleK8sLowGPUUtil(&inst)

	if len(inst.WasteSignals) != 0 {
		t.Errorf("expected no signals when metrics unavailable")
	}
}

func TestRuleK8sLowGPUUtil_SkipsHighUtilization(t *testing.T) {
	inst := models.GPUInstance{
		Source:            models.SourceK8sNode,
		State:             "ready",
		AvgGPUUtilization: ptr(45.0),
	}

	ruleK8sLowGPUUtil(&inst)

	if len(inst.WasteSignals) != 0 {
		t.Errorf("expected no signals for well-utilized GPU")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/analysis/ -run TestRuleK8sLowGPUUtil -v`
Expected: FAIL — `ruleK8sLowGPUUtil` not defined

- [ ] **Step 3: Implement the rule**

In `internal/analysis/rules.go`, add `ruleK8sLowGPUUtil` to the rules slice inside `analyzeInstance()` (line 23-31). The full slice should be:

```go
	rules := []func(*models.GPUInstance){
		ruleIdle,
		ruleOversizedGPU,
		rulePricingMismatch,
		ruleStale,
		ruleSageMakerLowUtil,
		ruleSageMakerOversized,
		ruleK8sUnallocatedGPU,
		ruleSpotEligible,
		ruleK8sLowGPUUtil,
	}
```

Then add the rule function at the end of the file:

```go
// Rule 9: K8s GPU node with low GPU utilization (requires DCGM/CW/Prometheus metrics).
func ruleK8sLowGPUUtil(inst *models.GPUInstance) {
	if inst.Source != models.SourceK8sNode {
		return
	}
	if inst.AvgGPUUtilization == nil {
		return
	}
	if *inst.AvgGPUUtilization >= 10 {
		return
	}

	inst.WasteSignals = append(inst.WasteSignals, models.WasteSignal{
		Type:       "low_utilization",
		Severity:   models.SeverityCritical,
		Confidence: 0.85,
		Evidence:   fmt.Sprintf("K8s GPU node utilization averaging %.1f%%. GPUs are allocated but barely used.", *inst.AvgGPUUtilization),
	})
	inst.Recommendations = append(inst.Recommendations, models.Recommendation{
		Action:                 models.ActionDownsize,
		Description:            fmt.Sprintf("GPU utilization averaging %.1f%%. Consider bin-packing more workloads, downsizing, or removing from the node pool.", *inst.AvgGPUUtilization),
		CurrentMonthlyCost:     inst.MonthlyCost,
		RecommendedMonthlyCost: inst.MonthlyCost * 0.2,
		MonthlySavings:         inst.MonthlyCost * 0.8,
		SavingsPercent:         80,
		Risk:                   models.RiskMedium,
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/analysis/ -run TestRuleK8sLowGPUUtil -v`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/analysis/rules.go internal/analysis/rules_test.go
git commit -m "Add ruleK8sLowGPUUtil for utilization-based K8s GPU waste detection"
```

---

### Task 6: Wire Everything into CLI and Scan Flow

**Files:**
- Modify: `cmd/gpuaudit/main.go`
- Modify: `internal/providers/k8s/scanner.go`

This task adds the `--prom-url` and `--prom-endpoint` CLI flags, passes them through to the K8s scan, wires CloudWatch Container Insights enrichment, and orchestrates the fallback chain in `main.go`.

- [ ] **Step 1: Extend K8s ScanOptions**

In `internal/providers/k8s/scanner.go`, change the `ScanOptions` struct (lines 20-23) from:

```go
type ScanOptions struct {
	Kubeconfig string
	Context    string
}
```

to:

```go
type ScanOptions struct {
	Kubeconfig   string
	Context      string
	PromURL      string
	PromEndpoint string
}
```

- [ ] **Step 2: Export BuildClient**

Add to `internal/providers/k8s/scanner.go` after the existing `buildClient` function:

```go
func BuildClientPublic(kubeconfigPath, contextName string) (K8sClient, string, error) {
	return buildClient(kubeconfigPath, contextName)
}
```

- [ ] **Step 3: Add CLI flags**

In `cmd/gpuaudit/main.go`, add the flag variables after `scanKubeContext` (around line 51):

```go
	scanPromURL      string
	scanPromEndpoint string
```

Add the flag registrations inside the first `init()` function, after the `--kube-context` flag (after line 73):

```go
	scanCmd.Flags().StringVar(&scanPromURL, "prom-url", "", "Prometheus URL for GPU metrics (e.g., https://prometheus.corp.example.com)")
	scanCmd.Flags().StringVar(&scanPromEndpoint, "prom-endpoint", "", "In-cluster Prometheus service as namespace/service:port (e.g., monitoring/prometheus:9090)")
```

- [ ] **Step 4: Add flag validation and wiring in runScan**

In `cmd/gpuaudit/main.go`, in the `runScan` function, add validation after `ctx := context.Background()` (line 84):

```go
	if scanPromURL != "" && scanPromEndpoint != "" {
		return fmt.Errorf("--prom-url and --prom-endpoint are mutually exclusive")
	}
```

Then modify the K8s scan section. Replace the block starting with `// Kubernetes API scan` (around lines 107-119) with:

```go
	// Kubernetes API scan
	if !scanSkipK8s {
		k8sOpts := k8sprovider.ScanOptions{
			Kubeconfig:   scanKubeconfig,
			Context:      scanKubeContext,
			PromURL:      scanPromURL,
			PromEndpoint: scanPromEndpoint,
		}
		k8sInstances, err := k8sprovider.Scan(ctx, k8sOpts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: Kubernetes scan failed: %v\n", err)
		} else if len(k8sInstances) > 0 {
			if !scanSkipMetrics {
				enrichK8sGPUMetrics(ctx, k8sInstances, k8sOpts, opts)
			}
			analysis.AnalyzeAll(k8sInstances)
			result.Instances = append(result.Instances, k8sInstances...)
			result.Summary = awsprovider.BuildSummary(result.Instances)
		}
	}
```

- [ ] **Step 5: Add the enrichK8sGPUMetrics helper function**

Add this function at the bottom of `cmd/gpuaudit/main.go`:

```go
func enrichK8sGPUMetrics(ctx context.Context, instances []models.GPUInstance, k8sOpts k8sprovider.ScanOptions, awsOpts awsprovider.ScanOptions) {
	// Source 1: CloudWatch Container Insights
	if len(instances) > 0 && instances[0].ClusterName != "" {
		cfgOpts := []func(*awsconfig.LoadOptions) error{}
		if awsOpts.Profile != "" {
			cfgOpts = append(cfgOpts, awsconfig.WithSharedConfigProfile(awsOpts.Profile))
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
		if err == nil {
			region := instances[0].Region
			if region == "" {
				region = "us-east-1"
			}
			cfg.Region = region
			cwClient := cloudwatch.NewFromConfig(cfg)
			fmt.Fprintf(os.Stderr, "  Enriching K8s GPU metrics via CloudWatch Container Insights...\n")
			awsprovider.EnrichK8sGPUMetrics(ctx, cwClient, instances, instances[0].ClusterName, awsprovider.DefaultMetricWindow)

			enriched := 0
			for _, inst := range instances {
				if inst.AvgGPUUtilization != nil {
					enriched++
				}
			}
			fmt.Fprintf(os.Stderr, "  CloudWatch: got GPU metrics for %d of %d nodes\n", enriched, len(instances))
		}
	}

	// Count remaining
	remaining := 0
	for _, inst := range instances {
		if inst.AvgGPUUtilization == nil {
			remaining++
		}
	}

	// Source 2: DCGM exporter scrape
	if remaining > 0 {
		client, _, err := k8sprovider.BuildClientPublic(k8sOpts.Kubeconfig, k8sOpts.Context)
		if err == nil {
			k8sprovider.EnrichDCGMMetrics(ctx, client, instances)
		}

		remaining = 0
		for _, inst := range instances {
			if inst.AvgGPUUtilization == nil {
				remaining++
			}
		}
	}

	// Source 3: Prometheus query
	if remaining > 0 && (k8sOpts.PromURL != "" || k8sOpts.PromEndpoint != "") {
		var client k8sprovider.K8sClient
		if k8sOpts.PromEndpoint != "" {
			c, _, err := k8sprovider.BuildClientPublic(k8sOpts.Kubeconfig, k8sOpts.Context)
			if err == nil {
				client = c
			}
		}
		promOpts := k8sprovider.PrometheusOptions{
			URL:      k8sOpts.PromURL,
			Endpoint: k8sOpts.PromEndpoint,
		}
		k8sprovider.EnrichPrometheusMetrics(ctx, client, instances, promOpts)
	}
}
```

You will need to add the `"github.com/aws/aws-sdk-go-v2/service/cloudwatch"` import to `main.go` if it's not already present.

- [ ] **Step 6: Run build and full test suite**

Run: `go build ./... && go test ./...`
Expected: Build succeeds, all tests pass

- [ ] **Step 7: Commit**

```bash
git add cmd/gpuaudit/main.go internal/providers/k8s/scanner.go
git commit -m "Wire K8s GPU metrics fallback chain into CLI scan flow"
```
