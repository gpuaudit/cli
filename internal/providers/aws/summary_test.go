package aws

import (
	"testing"

	"github.com/gpuaudit/cli/internal/models"
)

func TestBuildTargetSummaries_MultipleAccounts(t *testing.T) {
	instances := []models.GPUInstance{
		{
			AccountID:        "111111111111",
			MonthlyCost:      1000,
			EstimatedSavings: 500,
			WasteSignals:     []models.WasteSignal{{Severity: models.SeverityCritical}},
		},
		{
			AccountID:        "111111111111",
			MonthlyCost:      2000,
			EstimatedSavings: 0,
		},
		{
			AccountID:        "222222222222",
			MonthlyCost:      3000,
			EstimatedSavings: 1000,
			WasteSignals:     []models.WasteSignal{{Severity: models.SeverityWarning}},
		},
	}

	summaries := BuildTargetSummaries(instances)

	if len(summaries) != 2 {
		t.Fatalf("expected 2 target summaries, got %d", len(summaries))
	}

	var s1, s2 *models.TargetSummary
	for i := range summaries {
		switch summaries[i].Target {
		case "111111111111":
			s1 = &summaries[i]
		case "222222222222":
			s2 = &summaries[i]
		}
	}

	if s1 == nil || s2 == nil {
		t.Fatal("missing target summaries")
	}

	if s1.TotalInstances != 2 {
		t.Errorf("acct1: expected 2 instances, got %d", s1.TotalInstances)
	}
	if s1.TotalMonthlyCost != 3000 {
		t.Errorf("acct1: expected $3000 cost, got $%.0f", s1.TotalMonthlyCost)
	}
	if s1.TotalEstimatedWaste != 500 {
		t.Errorf("acct1: expected $500 waste, got $%.0f", s1.TotalEstimatedWaste)
	}
	if s1.CriticalCount != 1 {
		t.Errorf("acct1: expected 1 critical, got %d", s1.CriticalCount)
	}

	if s2.TotalInstances != 1 {
		t.Errorf("acct2: expected 1 instance, got %d", s2.TotalInstances)
	}
	if s2.WarningCount != 1 {
		t.Errorf("acct2: expected 1 warning, got %d", s2.WarningCount)
	}
}

func TestBuildTargetSummaries_SingleAccount(t *testing.T) {
	instances := []models.GPUInstance{
		{AccountID: "111111111111", MonthlyCost: 1000},
	}

	summaries := BuildTargetSummaries(instances)

	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
}

func TestBuildTargetSummaries_Empty(t *testing.T) {
	summaries := BuildTargetSummaries(nil)

	if len(summaries) != 0 {
		t.Fatalf("expected 0 summaries for nil input, got %d", len(summaries))
	}
}
