// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
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
		key := inst.K8sNodeName
		if key == "" {
			key = inst.InstanceID
		}
		needsMetrics[key] = i
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
	scrapeErrors := 0
	for _, pod := range dcgmPods {
		idx, ok := needsMetrics[pod.Spec.NodeName]
		if !ok {
			continue
		}

		data, err := client.ProxyGet(ctx, pod.Namespace, pod.Name, "9400", "/metrics")
		if err != nil {
			scrapeErrors++
			if scrapeErrors == 1 {
				fmt.Fprintf(os.Stderr, "  warning: DCGM scrape failed: %v\n", err)
			}
			if scrapeErrors >= 3 {
				fmt.Fprintf(os.Stderr, "  warning: DCGM scrape failing consistently, skipping remaining nodes\n")
				break
			}
			continue
		}

		gpuUtil, memUtil := parseDCGMMetrics(data)
		if gpuUtil != nil {
			instances[idx].AvgGPUUtilization = gpuUtil
			instances[idx].AvgGPUMemUtilization = memUtil
			enriched++
			scrapeErrors = 0
		}
	}

	if enriched > 0 {
		fmt.Fprintf(os.Stderr, "  DCGM: got GPU metrics for %d of %d remaining nodes\n", enriched, len(needsMetrics))
	}
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
	parser := expfmt.NewTextParser(model.LegacyValidation)
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
		name := inst.K8sNodeName
		if name == "" {
			name = inst.InstanceID
		}
		nodes = append(nodes, nodeRef{index: i, name: name})
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
