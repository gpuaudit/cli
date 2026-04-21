// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"encoding/csv"
	"fmt"
	"io"

	"github.com/gpuaudit/cli/internal/models"
)


func FormatCSV(w io.Writer, result *models.ScanResult) error {
	csvWriter := csv.NewWriter(w)

	if err := csvWriter.WriteAll(ToCSVRecords(result)); err != nil {
		fmt.Errorf("encoding csv: %w", err)
	}
	return nil
}


func ToCSVRecords(result *models.ScanResult) [][]string {
	results := make([][]string, len(result.Instances))

	for i, instance := range result.Instances {
			instance_id := instance.InstanceID
			name := instance.Name
			
			var source string
			switch instance.Source {
			case models.SourceEC2:
				source = "ec2"
			case models.SourceSageMakerEndpoint:
				source = "sagemaker-endpoint"
			case models.SourceSageMakerTraining:
				source = "sagemaker-training"
			case models.SourceEKS:
				source = "eks"
			case models.SourceK8sNode:
				source = "k8s-node"
			}

			region := instance.Region
			instance_type := instance.InstanceType
			gpu_model := instance.GPUModel
			gpu_count := fmt.Sprintf("%d", instance.GPUCount)
			state := instance.State
			monthly_cost := fmt.Sprintf("%.4f", instance.MonthlyCost)
			estimated_savings := fmt.Sprintf("%.4f", instance.EstimatedSavings)

			var severity string
			switch models.MaxSeverity(instance.WasteSignals) {
			case models.SeverityCritical:
				severity = "critical"
			case models.SeverityWarning:
				severity = "warning"
			case models.SeverityInfo:
				severity = "info"
			}

			signal_type := instance.WasteSignals[i].Type
			
			var recommendation string
			switch instance.Recommendations[i].Action {
			case models.ActionTerminate:
				recommendation = "terminate"
			case models.ActionDownsize:
				recommendation = "downsize"
			case models.ActionChangePricing:
				recommendation = "change_pricing"
			case models.ActionSchedule:
				recommendation = "schedule"
			case models.ActionInvestigate:
				recommendation = "investigate"
			}

			row := []string{instance_id, name, source, region, instance_type, gpu_model, gpu_count, state, monthly_cost, estimated_savings, severity, signal_type, recommendation }
			results = append(results, row)
	}
	return results
}
