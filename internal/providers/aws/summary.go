package aws

import (
	"sort"

	"github.com/gpuaudit/cli/internal/models"
)

// BuildSummary computes aggregate statistics for a set of GPU instances.
func BuildSummary(instances []models.GPUInstance) models.ScanSummary {
	s := models.ScanSummary{
		TotalInstances: len(instances),
	}

	for _, inst := range instances {
		s.TotalMonthlyCost += inst.MonthlyCost
		s.TotalEstimatedWaste += inst.EstimatedSavings

		maxSeverity := models.Severity("")
		for _, sig := range inst.WasteSignals {
			if sig.Severity == models.SeverityCritical {
				maxSeverity = models.SeverityCritical
			} else if sig.Severity == models.SeverityWarning && maxSeverity != models.SeverityCritical {
				maxSeverity = models.SeverityWarning
			} else if sig.Severity == models.SeverityInfo && maxSeverity == "" {
				maxSeverity = models.SeverityInfo
			}
		}

		switch maxSeverity {
		case models.SeverityCritical:
			s.CriticalCount++
		case models.SeverityWarning:
			s.WarningCount++
		case models.SeverityInfo:
			s.InfoCount++
		default:
			s.HealthyCount++
		}
	}

	if s.TotalMonthlyCost > 0 {
		s.WastePercent = (s.TotalEstimatedWaste / s.TotalMonthlyCost) * 100
	}

	return s
}

// BuildTargetSummaries computes per-target breakdowns from a flat instance list.
func BuildTargetSummaries(instances []models.GPUInstance) []models.TargetSummary {
	if len(instances) == 0 {
		return nil
	}

	byTarget := make(map[string][]models.GPUInstance)
	for _, inst := range instances {
		byTarget[inst.AccountID] = append(byTarget[inst.AccountID], inst)
	}

	summaries := make([]models.TargetSummary, 0, len(byTarget))
	for target, insts := range byTarget {
		ts := models.TargetSummary{
			Target:         target,
			TotalInstances: len(insts),
		}
		for _, inst := range insts {
			ts.TotalMonthlyCost += inst.MonthlyCost
			ts.TotalEstimatedWaste += inst.EstimatedSavings

			maxSev := models.Severity("")
			for _, sig := range inst.WasteSignals {
				if sig.Severity == models.SeverityCritical {
					maxSev = models.SeverityCritical
				} else if sig.Severity == models.SeverityWarning && maxSev != models.SeverityCritical {
					maxSev = models.SeverityWarning
				}
			}
			switch maxSev {
			case models.SeverityCritical:
				ts.CriticalCount++
			case models.SeverityWarning:
				ts.WarningCount++
			}
		}
		if ts.TotalMonthlyCost > 0 {
			ts.WastePercent = (ts.TotalEstimatedWaste / ts.TotalMonthlyCost) * 100
		}
		summaries = append(summaries, ts)
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].TotalMonthlyCost > summaries[j].TotalMonthlyCost
	})

	return summaries
}
