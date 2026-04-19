// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package analysis implements waste detection rules for GPU instances.
package analysis

import (
	"fmt"
	"strings"

	"github.com/gpuaudit/cli/internal/models"
	"github.com/gpuaudit/cli/internal/pricing"
)

// AnalyzeAll runs all waste detection rules against a set of GPU instances.
func AnalyzeAll(instances []models.GPUInstance) {
	for i := range instances {
		analyzeInstance(&instances[i])
	}
}

func analyzeInstance(inst *models.GPUInstance) {
	rules := []func(*models.GPUInstance){
		ruleIdle,
		ruleOversizedGPU,
		rulePricingMismatch,
		ruleStale,
		ruleSageMakerLowUtil,
		ruleSageMakerOversized,
		ruleK8sUnallocatedGPU,
		ruleK8sLowGPUUtil,
	}
	for _, rule := range rules {
		rule(inst)
	}

	// Compute total estimated savings
	for _, rec := range inst.Recommendations {
		if rec.MonthlySavings > inst.EstimatedSavings {
			inst.EstimatedSavings = rec.MonthlySavings
		}
	}
}

// Rule 1: Idle GPU Detection
// Signals when a GPU instance shows near-zero CPU and network activity.
func ruleIdle(inst *models.GPUInstance) {
	if inst.State != "running" && inst.State != "in-service" {
		return
	}
	if inst.UptimeHours < 24 {
		return
	}
	if inst.AvgCPUPercent == nil {
		return
	}

	avgCPU := *inst.AvgCPUPercent
	lowCPU := avgCPU < 3.0

	lowNetwork := true
	if inst.AvgNetworkInBytes != nil && inst.AvgNetworkOutBytes != nil {
		// More than 1MB/hr average means something is happening
		lowNetwork = *inst.AvgNetworkInBytes < 1_000_000 && *inst.AvgNetworkOutBytes < 1_000_000
	}

	if lowCPU && lowNetwork {
		confidence := 0.85
		if avgCPU < 1.0 {
			confidence = 0.95
		}

		inst.WasteSignals = append(inst.WasteSignals, models.WasteSignal{
			Type:       "idle",
			Severity:   models.SeverityCritical,
			Confidence: confidence,
			Evidence:   fmt.Sprintf("Average CPU %.1f%%, near-zero network I/O for %.0f+ hours. Instance appears idle.", avgCPU, inst.UptimeHours),
		})
		inst.Recommendations = append(inst.Recommendations, models.Recommendation{
			Action:                 models.ActionTerminate,
			Description:            fmt.Sprintf("Terminate idle instance. No significant activity detected over %d days.", int(inst.UptimeHours/24)),
			CurrentMonthlyCost:     inst.MonthlyCost,
			RecommendedMonthlyCost: 0,
			MonthlySavings:         inst.MonthlyCost,
			SavingsPercent:         100,
			Risk:                   models.RiskLow,
		})
	}
}

// Rule 2: Oversized GPU — multi-GPU instance likely running single-GPU workload.
func ruleOversizedGPU(inst *models.GPUInstance) {
	if inst.GPUCount < 4 {
		return
	}
	if inst.AvgCPUPercent == nil {
		return
	}
	// Low CPU + low network suggests the workload isn't distributing across GPUs
	if *inst.AvgCPUPercent > 15 {
		return
	}
	highNetOut := inst.AvgNetworkOutBytes != nil && *inst.AvgNetworkOutBytes > 50_000_000
	if highNetOut {
		return // high outbound traffic suggests distributed workload
	}

	spec := pricing.LookupEC2(inst.InstanceType)
	if spec == nil {
		spec = pricing.LookupSageMaker(inst.InstanceType)
	}
	if spec == nil {
		return
	}

	alts := pricing.SmallerAlternatives(*spec)
	if len(alts) == 0 {
		return
	}
	best := alts[0] // cheapest alternative
	bestMonthlyCost := best.OnDemandHourly * 730
	savings := inst.MonthlyCost - bestMonthlyCost

	inst.WasteSignals = append(inst.WasteSignals, models.WasteSignal{
		Type:       "oversized_gpu",
		Severity:   models.SeverityWarning,
		Confidence: 0.7,
		Evidence:   fmt.Sprintf("Running %d× %s but CPU at %.1f%% with low network — likely a single-GPU workload.", inst.GPUCount, inst.GPUModel, *inst.AvgCPUPercent),
	})
	inst.Recommendations = append(inst.Recommendations, models.Recommendation{
		Action:                 models.ActionDownsize,
		Description:            fmt.Sprintf("Consider %s (1× %s) at $%.2f/hr instead of $%.2f/hr.", best.InstanceType, best.GPUModel, best.OnDemandHourly, spec.OnDemandHourly),
		CurrentMonthlyCost:     inst.MonthlyCost,
		RecommendedMonthlyCost: bestMonthlyCost,
		MonthlySavings:         savings,
		SavingsPercent:         (savings / inst.MonthlyCost) * 100,
		Risk:                   models.RiskMedium,
	})
}

// Rule 3: On-demand for 30+ days when reserved would save.
func rulePricingMismatch(inst *models.GPUInstance) {
	if inst.PricingModel != "on-demand" {
		return
	}
	if inst.UptimeHours < 720 { // 30 days
		return
	}

	// 1-year RI typically saves ~37% for GPU instances
	riDiscount := 0.37
	riMonthlyCost := inst.MonthlyCost * (1 - riDiscount)
	savings := inst.MonthlyCost - riMonthlyCost

	inst.WasteSignals = append(inst.WasteSignals, models.WasteSignal{
		Type:       "pricing_mismatch",
		Severity:   models.SeverityWarning,
		Confidence: 0.9,
		Evidence:   fmt.Sprintf("Running on-demand for %d days. A 1-year Reserved Instance saves ~%.0f%%.", int(inst.UptimeHours/24), riDiscount*100),
	})
	inst.Recommendations = append(inst.Recommendations, models.Recommendation{
		Action:                 models.ActionChangePricing,
		Description:            fmt.Sprintf("Switch to 1-year RI to save ~$%.0f/mo (%.0f%%). Running on-demand for %d days.", savings, riDiscount*100, int(inst.UptimeHours/24)),
		CurrentMonthlyCost:     inst.MonthlyCost,
		RecommendedMonthlyCost: riMonthlyCost,
		MonthlySavings:         savings,
		SavingsPercent:         riDiscount * 100,
		Risk:                   models.RiskLow,
	})
}

// Rule 4: Stale / forgotten instances — running 90+ days in non-prod.
func ruleStale(inst *models.GPUInstance) {
	if inst.UptimeHours < 2160 { // 90 days
		return
	}

	// Check if this looks like non-production
	nonProd := false
	nameAndTags := strings.ToLower(inst.Name)
	for _, v := range inst.Tags {
		nameAndTags += " " + strings.ToLower(v)
	}
	for _, keyword := range []string{"dev", "test", "sandbox", "staging", "experiment", "tmp", "temp"} {
		if strings.Contains(nameAndTags, keyword) {
			nonProd = true
			break
		}
	}

	if !nonProd {
		return
	}

	days := int(inst.UptimeHours / 24)
	inst.WasteSignals = append(inst.WasteSignals, models.WasteSignal{
		Type:       "stale",
		Severity:   models.SeverityWarning,
		Confidence: 0.6,
		Evidence:   fmt.Sprintf("Non-production GPU instance running for %d days. Verify it is still needed.", days),
	})
	inst.Recommendations = append(inst.Recommendations, models.Recommendation{
		Action:                 models.ActionInvestigate,
		Description:            fmt.Sprintf("Running for %d days in what appears to be a non-production environment. Terminate if no longer needed.", days),
		CurrentMonthlyCost:     inst.MonthlyCost,
		RecommendedMonthlyCost: 0,
		MonthlySavings:         inst.MonthlyCost,
		SavingsPercent:         100,
		Risk:                   models.RiskMedium,
	})
}

// Rule 5: SageMaker endpoint with very low GPU utilization.
func ruleSageMakerLowUtil(inst *models.GPUInstance) {
	if inst.Source != models.SourceSageMakerEndpoint {
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
		Confidence: 0.9,
		Evidence:   fmt.Sprintf("SageMaker endpoint GPU utilization averaging %.1f%%. Significantly underutilized.", *inst.AvgGPUUtilization),
	})

	invocDetail := ""
	if inst.InvocationCount != nil {
		invocDetail = fmt.Sprintf(" (%d invocations in metric window)", *inst.InvocationCount)
	}

	inst.Recommendations = append(inst.Recommendations, models.Recommendation{
		Action:                 models.ActionDownsize,
		Description:            fmt.Sprintf("GPU utilization is %.1f%%%s. Consider a smaller instance type or serverless inference.", *inst.AvgGPUUtilization, invocDetail),
		CurrentMonthlyCost:     inst.MonthlyCost,
		RecommendedMonthlyCost: inst.MonthlyCost * 0.2, // rough estimate
		MonthlySavings:         inst.MonthlyCost * 0.8,
		SavingsPercent:         80,
		Risk:                   models.RiskMedium,
	})
}

// Rule 6: SageMaker endpoint on oversized GPU (low memory utilization).
func ruleSageMakerOversized(inst *models.GPUInstance) {
	if inst.Source != models.SourceSageMakerEndpoint {
		return
	}
	if inst.AvgGPUMemUtilization == nil {
		return
	}
	if *inst.AvgGPUMemUtilization >= 30 {
		return
	}
	if inst.GPUCount <= 1 {
		return
	}

	spec := pricing.LookupSageMaker(inst.InstanceType)
	if spec == nil {
		return
	}

	usedVRAM := inst.TotalVRAMGiB * (*inst.AvgGPUMemUtilization / 100)

	alts := pricing.SmallerAlternatives(*spec)
	if len(alts) == 0 {
		return
	}

	// Find smallest alternative that can fit the used VRAM
	var best *pricing.GPUSpec
	for i := range alts {
		if alts[i].TotalVRAMGiB >= usedVRAM*1.3 { // 30% headroom
			best = &alts[i]
			break
		}
	}
	if best == nil {
		best = &alts[len(alts)-1] // fallback to cheapest single-GPU
	}

	bestMonthlyCost := best.OnDemandHourly * 730
	savings := inst.MonthlyCost - bestMonthlyCost

	inst.WasteSignals = append(inst.WasteSignals, models.WasteSignal{
		Type:       "oversized_gpu",
		Severity:   models.SeverityWarning,
		Confidence: 0.75,
		Evidence:   fmt.Sprintf("GPU memory utilization averaging %.1f%% (using ~%.0f GiB of %.0f GiB). Model likely fits on smaller GPU.", *inst.AvgGPUMemUtilization, usedVRAM, inst.TotalVRAMGiB),
	})
	inst.Recommendations = append(inst.Recommendations, models.Recommendation{
		Action:                 models.ActionDownsize,
		Description:            fmt.Sprintf("Model uses ~%.0f GiB VRAM. Consider %s (%.0f GiB) at $%.2f/hr.", usedVRAM, best.InstanceType, best.TotalVRAMGiB, best.OnDemandHourly),
		CurrentMonthlyCost:     inst.MonthlyCost,
		RecommendedMonthlyCost: bestMonthlyCost,
		MonthlySavings:         savings,
		SavingsPercent:         (savings / inst.MonthlyCost) * 100,
		Risk:                   models.RiskMedium,
	})
}

// Rule 7: K8s node with unallocated GPU capacity.
func ruleK8sUnallocatedGPU(inst *models.GPUInstance) {
	if inst.Source != models.SourceK8sNode {
		return
	}
	if inst.State != "ready" {
		return
	}

	if inst.GPUAllocated == 0 && inst.UptimeHours >= 24 {
		inst.WasteSignals = append(inst.WasteSignals, models.WasteSignal{
			Type:       "idle",
			Severity:   models.SeverityCritical,
			Confidence: 0.9,
			Evidence:   fmt.Sprintf("GPU node has %d GPU(s) but no pods requesting GPUs for %.0f+ hours.", inst.GPUCount, inst.UptimeHours),
		})
		inst.Recommendations = append(inst.Recommendations, models.Recommendation{
			Action:             models.ActionTerminate,
			Description:        fmt.Sprintf("No GPU pods scheduled on this node for %d days. Remove from node pool or scale down.", int(inst.UptimeHours/24)),
			CurrentMonthlyCost: inst.MonthlyCost,
			MonthlySavings:     inst.MonthlyCost,
			SavingsPercent:     100,
			Risk:               models.RiskLow,
		})
	} else if inst.GPUAllocated > 0 && inst.GPUAllocated < inst.GPUCount {
		unused := inst.GPUCount - inst.GPUAllocated
		wastedCost := inst.MonthlyCost * float64(unused) / float64(inst.GPUCount)
		inst.WasteSignals = append(inst.WasteSignals, models.WasteSignal{
			Type:       "low_utilization",
			Severity:   models.SeverityWarning,
			Confidence: 0.8,
			Evidence:   fmt.Sprintf("Only %d of %d GPUs allocated to pods. %d GPU(s) sitting idle.", inst.GPUAllocated, inst.GPUCount, unused),
		})
		inst.Recommendations = append(inst.Recommendations, models.Recommendation{
			Action:                 models.ActionDownsize,
			Description:            fmt.Sprintf("Node has %d unused GPU(s). Consider a smaller instance or bin-packing more workloads.", unused),
			CurrentMonthlyCost:     inst.MonthlyCost,
			RecommendedMonthlyCost: inst.MonthlyCost - wastedCost,
			MonthlySavings:         wastedCost,
			SavingsPercent:         (wastedCost / inst.MonthlyCost) * 100,
			Risk:                   models.RiskMedium,
		})
	}
}

// Rule 8: K8s GPU node with low GPU utilization (requires DCGM/CW/Prometheus metrics).
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
		Evidence:   fmt.Sprintf("K8s GPU node utilization averaging %.1f%% over the past 7 days. GPUs are allocated but barely used.", *inst.AvgGPUUtilization),
	})
	inst.Recommendations = append(inst.Recommendations, models.Recommendation{
		Action:                 models.ActionDownsize,
		Description:            fmt.Sprintf("GPU utilization averaging %.1f%% over the past 7 days. Consider bin-packing more workloads, downsizing, or removing from the node pool.", *inst.AvgGPUUtilization),
		CurrentMonthlyCost:     inst.MonthlyCost,
		RecommendedMonthlyCost: inst.MonthlyCost * 0.2,
		MonthlySavings:         inst.MonthlyCost * 0.8,
		SavingsPercent:         80,
		Risk:                   models.RiskMedium,
	})
}
