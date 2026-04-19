// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package models defines the core data types for gpuaudit.
package models

import "time"

// Source identifies where a GPU instance was discovered.
type Source string

const (
	SourceEC2               Source = "ec2"
	SourceSageMakerEndpoint Source = "sagemaker-endpoint"
	SourceSageMakerTraining Source = "sagemaker-training"
	SourceEKS               Source = "eks"
	SourceK8sNode           Source = "k8s-node"
)

// Severity indicates how urgent a waste signal is.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// Action describes what the user should do to save money.
type Action string

const (
	ActionTerminate    Action = "terminate"
	ActionDownsize     Action = "downsize"
	ActionChangePricing Action = "change_pricing"
	ActionSchedule     Action = "schedule"
	ActionInvestigate  Action = "investigate"
)

// Risk level for a recommendation.
type Risk string

const (
	RiskNone   Risk = "none"
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

// GPUInstance represents a discovered GPU resource with its metrics and cost data.
type GPUInstance struct {
	// Identity
	InstanceID string            `json:"instance_id"`
	Source     Source            `json:"source"`
	AccountID  string            `json:"account_id"`
	Region     string            `json:"region"`
	Name       string            `json:"name"` // from Name tag or endpoint name
	Tags       map[string]string `json:"tags,omitempty"`

	// GPU hardware
	InstanceType  string  `json:"instance_type"`
	GPUModel      string  `json:"gpu_model"`
	GPUCount      int     `json:"gpu_count"`
	GPUVRAMGiB    float64 `json:"gpu_vram_gib"`
	TotalVRAMGiB  float64 `json:"total_vram_gib"`

	// Kubernetes (populated for k8s-node source)
	ClusterName  string `json:"cluster_name,omitempty"`
	K8sNodeName  string `json:"k8s_node_name,omitempty"`
	GPUAllocated int    `json:"gpu_allocated,omitempty"`

	// State
	State       string    `json:"state"`
	LaunchTime  time.Time `json:"launch_time"`
	UptimeHours float64   `json:"uptime_hours"`

	// Metrics (nil means unavailable)
	AvgCPUPercent          *float64 `json:"avg_cpu_percent,omitempty"`
	MaxCPUPercent          *float64 `json:"max_cpu_percent,omitempty"`
	AvgNetworkInBytes      *float64 `json:"avg_network_in_bytes,omitempty"`
	AvgNetworkOutBytes     *float64 `json:"avg_network_out_bytes,omitempty"`
	AvgDiskReadOps         *float64 `json:"avg_disk_read_ops,omitempty"`
	AvgDiskWriteOps        *float64 `json:"avg_disk_write_ops,omitempty"`
	AvgGPUUtilization      *float64 `json:"avg_gpu_utilization,omitempty"`
	AvgGPUMemUtilization   *float64 `json:"avg_gpu_mem_utilization,omitempty"`
	InvocationCount        *int64   `json:"invocation_count,omitempty"`

	// Cost
	PricingModel string  `json:"pricing_model"` // on-demand, spot, reserved, savings-plan
	HourlyCost   float64 `json:"hourly_cost"`
	MonthlyCost  float64 `json:"monthly_cost"`
	MTDCost      *float64 `json:"mtd_cost,omitempty"`

	// Analysis results (populated by analysis engine)
	WasteSignals    []WasteSignal    `json:"waste_signals,omitempty"`
	Recommendations []Recommendation `json:"recommendations,omitempty"`
	EstimatedSavings float64         `json:"estimated_savings"`
}

// WasteSignal represents a detected waste indicator on a GPU instance.
type WasteSignal struct {
	Type       string   `json:"type"` // idle, low_utilization, oversized_gpu, pricing_mismatch, stale, low_invocations
	Severity   Severity `json:"severity"`
	Confidence float64  `json:"confidence"` // 0.0 - 1.0
	Evidence   string   `json:"evidence"`
}

// Recommendation is a specific action the user can take to reduce cost.
type Recommendation struct {
	Action              Action  `json:"action"`
	Description         string  `json:"description"`
	CurrentMonthlyCost  float64 `json:"current_monthly_cost"`
	RecommendedMonthlyCost float64 `json:"recommended_monthly_cost"`
	MonthlySavings      float64 `json:"monthly_savings"`
	SavingsPercent      float64 `json:"savings_percent"`
	Risk                Risk    `json:"risk"`
}

// ScanResult holds the complete output of a gpuaudit scan.
type ScanResult struct {
	Timestamp     time.Time     `json:"timestamp"`
	AccountID     string        `json:"account_id"`
	Regions       []string      `json:"regions"`
	ScanDuration  string        `json:"scan_duration"`
	Instances     []GPUInstance `json:"instances"`
	Summary       ScanSummary   `json:"summary"`
}

// ScanSummary provides aggregate statistics for a scan.
type ScanSummary struct {
	TotalInstances    int     `json:"total_instances"`
	TotalMonthlyCost  float64 `json:"total_monthly_cost"`
	TotalEstimatedWaste float64 `json:"total_estimated_waste"`
	WastePercent      float64 `json:"waste_percent"`
	CriticalCount     int     `json:"critical_count"`
	WarningCount      int     `json:"warning_count"`
	InfoCount         int     `json:"info_count"`
	HealthyCount      int     `json:"healthy_count"`
}

// Ptr is a convenience helper for creating pointer values in tests and literals.
func Ptr[T any](v T) *T { return &v }
