package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/maksimov/gpuaudit/internal/models"
)

// FormatMarkdown writes the scan result as a Markdown report.
func FormatMarkdown(w io.Writer, result *models.ScanResult) {
	s := result.Summary

	fmt.Fprintf(w, "# GPU Cost Audit Report\n\n")
	fmt.Fprintf(w, "**Account:** %s | **Regions:** %s | **Scanned:** %s\n\n",
		result.AccountID,
		strings.Join(result.Regions, ", "),
		result.Timestamp.Format("2006-01-02 15:04 UTC"),
	)

	fmt.Fprintf(w, "## Summary\n\n")
	fmt.Fprintf(w, "| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(w, "| Total GPU instances | %d |\n", s.TotalInstances)
	fmt.Fprintf(w, "| Total monthly GPU spend | $%.0f |\n", s.TotalMonthlyCost)
	fmt.Fprintf(w, "| Estimated monthly waste | $%.0f (%.0f%%) |\n", s.TotalEstimatedWaste, s.WastePercent)
	fmt.Fprintf(w, "| Critical | %d |\n", s.CriticalCount)
	fmt.Fprintf(w, "| Warning | %d |\n", s.WarningCount)
	fmt.Fprintf(w, "| Healthy | %d |\n\n", s.HealthyCount)

	if s.TotalInstances == 0 {
		fmt.Fprintf(w, "No GPU instances found.\n")
		return
	}

	fmt.Fprintf(w, "## Findings\n\n")
	fmt.Fprintf(w, "| Instance | Type | Monthly Cost | Signal | Savings | Recommendation |\n")
	fmt.Fprintf(w, "|---|---|---|---|---|---|\n")

	for _, inst := range result.Instances {
		name := inst.Name
		if name == "" {
			name = inst.InstanceID
		}

		signal := ""
		if len(inst.WasteSignals) > 0 {
			signal = fmt.Sprintf("%s (%s)", inst.WasteSignals[0].Type, inst.WasteSignals[0].Severity)
		}

		rec := ""
		if len(inst.Recommendations) > 0 {
			rec = inst.Recommendations[0].Description
		}

		savings := ""
		if inst.EstimatedSavings > 0 {
			savings = fmt.Sprintf("$%.0f/mo", inst.EstimatedSavings)
		}

		fmt.Fprintf(w, "| %s | %s (%d× %s) | $%.0f | %s | %s | %s |\n",
			name, inst.InstanceType, inst.GPUCount, inst.GPUModel,
			inst.MonthlyCost, signal, savings, rec)
	}
}
