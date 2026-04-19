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
