// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package k8s implements GPU resource discovery via the Kubernetes API.
package k8s

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gpuaudit/cli/internal/models"
	"github.com/gpuaudit/cli/internal/pricing"
)

const gpuResourceName corev1.ResourceName = "nvidia.com/gpu"

// K8sClient is the subset of the Kubernetes API needed for GPU discovery.
type K8sClient interface {
	ListNodes(ctx context.Context, opts metav1.ListOptions) (*corev1.NodeList, error)
	ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error)
	ProxyGet(ctx context.Context, namespace, podName, port, path string) ([]byte, error)
}

// DiscoverGPUNodes finds Kubernetes nodes with GPU capacity and reports their allocation.
func DiscoverGPUNodes(ctx context.Context, client K8sClient, clusterName string) ([]models.GPUInstance, error) {
	nodeList, err := client.ListNodes(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	// Find GPU nodes
	var gpuNodes []corev1.Node
	for _, node := range nodeList.Items {
		if gpuCount := nodeGPUCount(node); gpuCount > 0 {
			gpuNodes = append(gpuNodes, node)
		}
	}

	fmt.Fprintf(os.Stderr, "  Found %d GPU nodes across %d nodes in %s\n", len(gpuNodes), len(nodeList.Items), clusterName)

	if len(gpuNodes) == 0 {
		return nil, nil
	}

	// List all pods once, group GPU-requesting pods by node
	podList, err := client.ListPods(ctx, "", metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	podsByNode := make(map[string][]corev1.Pod)
	for _, pod := range podList.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		if podGPURequests(pod) > 0 {
			podsByNode[pod.Spec.NodeName] = append(podsByNode[pod.Spec.NodeName], pod)
		}
	}

	var instances []models.GPUInstance
	for _, node := range gpuNodes {
		inst := nodeToGPUInstance(node, podsByNode[node.Name], clusterName)
		if inst != nil {
			instances = append(instances, *inst)
		}
	}

	return instances, nil
}

func nodeToGPUInstance(node corev1.Node, gpuPods []corev1.Pod, clusterName string) *models.GPUInstance {
	gpuCount := nodeGPUCount(node)
	if gpuCount == 0 {
		return nil
	}

	instanceType := node.Labels["node.kubernetes.io/instance-type"]

	// Try to get GPU specs from the instance type
	var gpuModel string
	var gpuVRAMGiB, totalVRAMGiB float64
	var hourlyCost float64
	if spec := pricing.LookupEC2(instanceType); spec != nil {
		gpuModel = spec.GPUModel
		gpuVRAMGiB = spec.GPUVRAMGiB
		totalVRAMGiB = spec.TotalVRAMGiB
		hourlyCost = spec.OnDemandHourly
		gpuCount = spec.GPUCount // trust pricing DB over node allocatable
	} else {
		// Fall back to node labels for GPU model identification
		for _, labelKey := range []string{
			"nvidia.com/gpu.product",              // NVIDIA GPU Operator
			"karpenter.k8s.aws/instance-gpu-name", // Karpenter on AWS
		} {
			if v, ok := node.Labels[labelKey]; ok && v != "" {
				gpuModel = strings.ToUpper(v)
				break
			}
		}
	}

	// Instance ID: prefer EC2 instance ID from providerID, fall back to node name
	instanceID := extractEC2InstanceID(node.Spec.ProviderID)
	if instanceID == "" {
		instanceID = node.Name
	}

	// Determine region from topology label
	region := node.Labels["topology.kubernetes.io/region"]

	// Calculate GPU allocation from pods
	var gpuAllocated int
	var podNames []string
	for _, pod := range gpuPods {
		gpuAllocated += int(podGPURequests(pod))
		podNames = append(podNames, fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
	}

	now := time.Now()
	creationTime := node.CreationTimestamp.Time
	uptimeHours := now.Sub(creationTime).Hours()

	tags := make(map[string]string)
	// Include useful node labels as tags
	for _, key := range []string{
		"karpenter.sh/nodepool",
		"eks.amazonaws.com/nodegroup",
		"node.kubernetes.io/instance-type",
	} {
		if v, ok := node.Labels[key]; ok {
			tags[key] = v
		}
	}
	if len(podNames) > 0 {
		tags["k8s.io/gpu-pods"] = strings.Join(podNames, ", ")
	}

	// Determine state from node conditions
	state := "not-ready"
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			state = "ready"
			break
		}
	}

	// Use short hostname (strip .ec2.internal etc.)
	hostname := node.Name
	if idx := strings.IndexByte(hostname, '.'); idx > 0 {
		hostname = hostname[:idx]
	}

	return &models.GPUInstance{
		InstanceID:   instanceID,
		Source:       models.SourceK8sNode,
		Region:       region,
		Name:         fmt.Sprintf("%s/%s", clusterName, hostname),
		Tags:         tags,
		ClusterName:  clusterName,
		GPUAllocated: gpuAllocated,
		InstanceType: instanceType,
		GPUModel:     gpuModel,
		GPUCount:     gpuCount,
		GPUVRAMGiB:   gpuVRAMGiB,
		TotalVRAMGiB: totalVRAMGiB,
		State:        state,
		LaunchTime:   creationTime,
		UptimeHours:  uptimeHours,
		PricingModel: "on-demand",
		HourlyCost:   hourlyCost,
		MonthlyCost:  hourlyCost * 730,
	}
}

func nodeGPUCount(node corev1.Node) int {
	q, ok := node.Status.Allocatable[gpuResourceName]
	if !ok {
		return 0
	}
	return int(q.Value())
}

func podGPURequests(pod corev1.Pod) int64 {
	var total int64
	for _, c := range pod.Spec.Containers {
		if q, ok := c.Resources.Requests[gpuResourceName]; ok {
			total += q.Value()
		}
	}
	for _, c := range pod.Spec.InitContainers {
		if q, ok := c.Resources.Requests[gpuResourceName]; ok {
			total += q.Value()
		}
	}
	return total
}

// extractEC2InstanceID parses the EC2 instance ID from a Kubernetes node providerID.
// Format: "aws:///us-east-1a/i-0123456789abcdef0"
func extractEC2InstanceID(providerID string) string {
	if !strings.HasPrefix(providerID, "aws://") {
		return ""
	}
	parts := strings.Split(providerID, "/")
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	if strings.HasPrefix(last, "i-") {
		return last
	}
	return ""
}

