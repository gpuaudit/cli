// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/gpuaudit/cli/internal/models"
)

// CloudWatchClient is the subset of the CloudWatch API we need.
type CloudWatchClient interface {
	GetMetricData(ctx context.Context, params *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
}

// MetricWindow controls how far back we look for metrics.
type MetricWindow struct {
	Duration time.Duration
	Period   int32 // seconds
}

// DefaultMetricWindow looks back 7 days with 1-hour resolution.
var DefaultMetricWindow = MetricWindow{
	Duration: 7 * 24 * time.Hour,
	Period:   3600,
}

// EnrichEC2Metrics populates CloudWatch metrics on EC2 GPU instances.
func EnrichEC2Metrics(ctx context.Context, client CloudWatchClient, instances []models.GPUInstance, window MetricWindow) error {
	for i := range instances {
		inst := &instances[i]
		if inst.Source != models.SourceEC2 || inst.State != "running" {
			continue
		}

		metrics, err := getEC2Metrics(ctx, client, inst.InstanceID, window)
		if err != nil {
			fmt.Fprintf(os.Stderr,"  warning: metrics unavailable for %s: %v\n", inst.InstanceID, err)
			continue
		}

		inst.AvgCPUPercent = metrics["avg_cpu"]
		inst.MaxCPUPercent = metrics["max_cpu"]
		inst.AvgNetworkInBytes = metrics["avg_net_in"]
		inst.AvgNetworkOutBytes = metrics["avg_net_out"]
		inst.AvgDiskReadOps = metrics["avg_disk_read"]
		inst.AvgDiskWriteOps = metrics["avg_disk_write"]
	}
	return nil
}

// EnrichSageMakerMetrics populates GPU utilization metrics on SageMaker endpoints.
func EnrichSageMakerMetrics(ctx context.Context, client CloudWatchClient, instances []models.GPUInstance, window MetricWindow) error {
	for i := range instances {
		inst := &instances[i]
		if inst.Source != models.SourceSageMakerEndpoint {
			continue
		}

		metrics, err := getSageMakerMetrics(ctx, client, inst.Name, window)
		if err != nil {
			fmt.Fprintf(os.Stderr,"  warning: metrics unavailable for SageMaker endpoint %s: %v\n", inst.Name, err)
			continue
		}

		inst.AvgGPUUtilization = metrics["avg_gpu_util"]
		inst.AvgGPUMemUtilization = metrics["avg_gpu_mem_util"]
		inst.AvgCPUPercent = metrics["avg_cpu"]
		inst.InvocationCount = intPtrFromFloat(metrics["sum_invocations"])
	}
	return nil
}

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

func getEC2Metrics(ctx context.Context, client CloudWatchClient, instanceID string, window MetricWindow) (map[string]*float64, error) {
	now := time.Now()
	start := now.Add(-window.Duration)

	dim := cwtypes.Dimension{
		Name:  aws.String("InstanceId"),
		Value: aws.String(instanceID),
	}

	queries := []cwtypes.MetricDataQuery{
		metricQuery("avg_cpu", "AWS/EC2", "CPUUtilization", "Average", dim, window.Period),
		metricQuery("max_cpu", "AWS/EC2", "CPUUtilization", "Maximum", dim, window.Period),
		metricQuery("avg_net_in", "AWS/EC2", "NetworkIn", "Average", dim, window.Period),
		metricQuery("avg_net_out", "AWS/EC2", "NetworkOut", "Average", dim, window.Period),
		metricQuery("avg_disk_read", "AWS/EC2", "DiskReadOps", "Average", dim, window.Period),
		metricQuery("avg_disk_write", "AWS/EC2", "DiskWriteOps", "Average", dim, window.Period),
	}

	return fetchMetrics(ctx, client, queries, start, now)
}

func getSageMakerMetrics(ctx context.Context, client CloudWatchClient, endpointName string, window MetricWindow) (map[string]*float64, error) {
	now := time.Now()
	start := now.Add(-window.Duration)

	dim := cwtypes.Dimension{
		Name:  aws.String("EndpointName"),
		Value: aws.String(endpointName),
	}
	variantDim := cwtypes.Dimension{
		Name:  aws.String("VariantName"),
		Value: aws.String("primary"), // TODO: support multiple variants
	}

	queries := []cwtypes.MetricDataQuery{
		metricQuery2("avg_gpu_util", "/aws/sagemaker/Endpoints", "GPUUtilization", "Average", window.Period, dim, variantDim),
		metricQuery2("avg_gpu_mem_util", "/aws/sagemaker/Endpoints", "GPUMemoryUtilization", "Average", window.Period, dim, variantDim),
		metricQuery2("avg_cpu", "/aws/sagemaker/Endpoints", "CPUUtilization", "Average", window.Period, dim, variantDim),
		metricQuery2("sum_invocations", "AWS/SageMaker", "Invocations", "Sum", window.Period, dim, variantDim),
	}

	return fetchMetrics(ctx, client, queries, start, now)
}

func metricQuery(id, namespace, metricName, stat string, dim cwtypes.Dimension, period int32) cwtypes.MetricDataQuery {
	return cwtypes.MetricDataQuery{
		Id: aws.String(id),
		MetricStat: &cwtypes.MetricStat{
			Metric: &cwtypes.Metric{
				Namespace:  aws.String(namespace),
				MetricName: aws.String(metricName),
				Dimensions: []cwtypes.Dimension{dim},
			},
			Period: aws.Int32(period),
			Stat:   aws.String(stat),
		},
	}
}

func metricQuery2(id, namespace, metricName, stat string, period int32, dims ...cwtypes.Dimension) cwtypes.MetricDataQuery {
	return cwtypes.MetricDataQuery{
		Id: aws.String(id),
		MetricStat: &cwtypes.MetricStat{
			Metric: &cwtypes.Metric{
				Namespace:  aws.String(namespace),
				MetricName: aws.String(metricName),
				Dimensions: dims,
			},
			Period: aws.Int32(period),
			Stat:   aws.String(stat),
		},
	}
}

func fetchMetrics(ctx context.Context, client CloudWatchClient, queries []cwtypes.MetricDataQuery, start, end time.Time) (map[string]*float64, error) {
	out, err := client.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		MetricDataQueries: queries,
		StartTime:         aws.Time(start),
		EndTime:           aws.Time(end),
	})
	if err != nil {
		return nil, err
	}

	results := make(map[string]*float64)
	for _, result := range out.MetricDataResults {
		id := aws.ToString(result.Id)
		if len(result.Values) == 0 {
			results[id] = nil
			continue
		}
		avg := average(result.Values)
		results[id] = &avg
	}
	return results, nil
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func intPtrFromFloat(f *float64) *int64 {
	if f == nil {
		return nil
	}
	v := int64(*f)
	return &v
}
