// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gpuaudit/cli/internal/models"
	prom "github.com/gpuaudit/cli/internal/prometheus"
)

// EnrichEC2PrometheusGPUMetrics queries a Prometheus endpoint for DCGM GPU metrics
// on EC2 instances that don't already have AvgGPUUtilization populated.
// It matches Prometheus results to EC2 instances via private DNS hostname.
func EnrichEC2PrometheusGPUMetrics(ctx context.Context, promURL string, instances []models.GPUInstance) int {
	if promURL == "" {
		return 0
	}

	type instRef struct {
		index    int
		hostname string
		ip       string
	}
	var refs []instRef
	for i := range instances {
		inst := &instances[i]
		if inst.Source != models.SourceEC2 || inst.State != "running" {
			continue
		}
		if inst.AvgGPUUtilization != nil {
			continue
		}
		if inst.PrivateDnsName == "" {
			continue
		}
		hostname := strings.SplitN(inst.PrivateDnsName, ".", 2)[0]
		ip := extractIPFromDNS(inst.PrivateDnsName)
		refs = append(refs, instRef{index: i, hostname: hostname, ip: ip})
	}
	if len(refs) == 0 {
		return 0
	}

	// Build lookup maps: hostname → index, ip → index
	hostnameToIdx := make(map[string]int, len(refs))
	ipToIdx := make(map[string]int, len(refs))
	for _, ref := range refs {
		hostnameToIdx[ref.hostname] = ref.index
		if ref.ip != "" {
			ipToIdx[ref.ip] = ref.index
		}
	}

	fmt.Fprintf(os.Stderr, "  Querying Prometheus at %s for EC2 GPU metrics...\n", promURL)

	// Query GPU utilization — get all DCGM metrics and match locally.
	// DCGM exporter labels vary by setup: "Hostname" for host identity,
	// "instance" for scrape target (ip:port).
	gpuByHostname, err := prom.QueryHTTP(ctx, promURL,
		`avg by (Hostname) (avg_over_time(DCGM_FI_DEV_GPU_UTIL[7d]))`, "Hostname")
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: Prometheus EC2 GPU query failed: %v\n", err)
		return 0
	}

	memByHostname, _ := prom.QueryHTTP(ctx, promURL,
		`avg by (Hostname) (avg_over_time(DCGM_FI_DEV_MEM_COPY_UTIL[7d]))`, "Hostname")

	enriched := 0

	// First pass: match by Hostname label (short hostname like "ip-10-22-249-234")
	for _, ref := range refs {
		if val, ok := gpuByHostname[ref.hostname]; ok {
			instances[ref.index].AvgGPUUtilization = &val
			if memVal, ok := memByHostname[ref.hostname]; ok {
				instances[ref.index].AvgGPUMemUtilization = &memVal
			}
			enriched++
		}
	}

	// Second pass: try matching by instance label (ip:port) for instances still missing metrics
	instanceSeriesCount := 0
	if enriched < len(refs) {
		gpuByInstance, err := prom.QueryHTTP(ctx, promURL,
			`avg by (instance) (avg_over_time(DCGM_FI_DEV_GPU_UTIL[7d]))`, "instance")
		if err == nil {
			instanceSeriesCount = len(gpuByInstance)
			memByInstance, _ := prom.QueryHTTP(ctx, promURL,
				`avg by (instance) (avg_over_time(DCGM_FI_DEV_MEM_COPY_UTIL[7d]))`, "instance")

			for instanceLabel, val := range gpuByInstance {
				ip := strings.SplitN(instanceLabel, ":", 2)[0]
				idx, ok := ipToIdx[ip]
				if !ok || instances[idx].AvgGPUUtilization != nil {
					continue
				}
				v := val
				instances[idx].AvgGPUUtilization = &v
				if memVal, ok := memByInstance[instanceLabel]; ok {
					instances[idx].AvgGPUMemUtilization = &memVal
				}
				enriched++
			}
		}
	}

	if enriched > 0 {
		fmt.Fprintf(os.Stderr, "  Prometheus: matched %d of %d EC2 instances\n", enriched, len(refs))
	} else {
		fmt.Fprintf(os.Stderr, "  Prometheus: matched 0 of %d EC2 instances (server returned %d Hostname series, %d instance series)\n",
			len(refs), len(gpuByHostname), instanceSeriesCount)
	}
	return enriched
}

// extractIPFromDNS extracts the IP address from an EC2 private DNS name.
// e.g., "ip-10-22-249-234.ec2.internal" → "10.22.249.234"
func extractIPFromDNS(dnsName string) string {
	hostname := strings.SplitN(dnsName, ".", 2)[0]
	if !strings.HasPrefix(hostname, "ip-") {
		return ""
	}
	return strings.ReplaceAll(hostname[3:], "-", ".")
}
