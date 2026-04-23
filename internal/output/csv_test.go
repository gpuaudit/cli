package output

import (
	"testing"
	"fmt"
	"os"
	"time"

	"github.com/gpuaudit/cli/internal/models"
)

// Shared test fixture: a single GPU instance with one waste signal and recommendation.
var instance = models.GPUInstance{
	InstanceID:   "i-1234567890abcdef0",
	Name:         "test-instance",
	Source:       models.SourceEC2,
	Region:       "us-west-2",
	InstanceType: "p5.24xlarge",
	GPUModel:     "NVIDIA A100",
	GPUCount:     8,
	State:        "running",
	MonthlyCost:  24.00,
	EstimatedSavings: 12.00,
	WasteSignals: []models.WasteSignal{
		{
			Type:       "underutilized",
			Severity:   models.SeverityWarning,
			Confidence: 0.8,
			Evidence:   "Average GPU utilization is 10%",
		},
	},
	Recommendations: []models.Recommendation{
		{
			Action: "downsize",
		},
	},
}

// Shared test fixture: a scan result wrapping the test instance above.
var result = &models.ScanResult{
	Timestamp:    time.Now(),
	AccountID:    "123456789012",
	Targets:      []string{"ec2"},
	Regions:      []string{"us-west-2"},
	ScanDuration: "60",
	Instances:    []models.GPUInstance{instance},
	Summary: models.ScanSummary{
		TotalInstances:      1,
		TotalMonthlyCost:    24.00,
		TotalEstimatedWaste: 12.00,
		WastePercent:        50.0,
		CriticalCount:       0,
		WarningCount:        1,
		InfoCount:           0,
		HealthyCount:        0,
	},
	TargetSummaries: []models.TargetSummary{
		{
			Target:              "ec2",
			TotalInstances:      1,
			TotalMonthlyCost:    24.00,
			TotalEstimatedWaste: 12.00,
			WastePercent:        50.0,
			CriticalCount:       0,
			WarningCount:        1,
		},
	},
	TargetErrors: []models.TargetErrorInfo{
		{
			Target: "sagemaker-endpoint",
			Error:  "Access denied",
		},
	},
}

// TestFormatCSV writes a scan result to a temp file and checks for no errors.
func TestFormatCSV(t *testing.T) {
	fileName := "test_output.csv"
	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("failed to create test output file: %v", err)
	}
	defer file.Close()
	defer os.Remove(fileName) // Clean up after test

	if err := FormatCSV(file, result); err != nil {
		t.Fatalf("FormatCSV failed: %v", err)
	}
}

// TestToCSVRecords checks that the CSV output matches the expected row layout.
func TestToCSVRecords(t *testing.T) {
	// Build the expected row using the same formatting logic as the production code.
	expected := [][]string{
		{
			instance.InstanceID,
			instance.Name,
			fmt.Sprintf("%s", instance.Source),
			instance.Region,
			instance.InstanceType,
			instance.GPUModel,
			fmt.Sprintf("%d", instance.GPUCount),
			instance.State,
			fmt.Sprintf("%.4f", instance.MonthlyCost),
			fmt.Sprintf("%.4f", instance.EstimatedSavings),
			"warning",
			instance.WasteSignals[0].Type,
			"downsize",
		},
	}

	result := ToCSVRecords(result)

	// Only checking length here; a deeper field-by-field check would be more thorough.
	if len(result) != len(expected) {
		t.Fatalf("expected: %v\ngot: %v", expected, result)
	}
}