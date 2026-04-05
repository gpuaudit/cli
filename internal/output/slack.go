// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/gpuaudit/gpuaudit/internal/models"
)

// FormatSlack writes a Slack Block Kit message JSON payload.
func FormatSlack(w io.Writer, result *models.ScanResult) error {
	s := result.Summary

	var blocks []map[string]any

	// Header
	blocks = append(blocks, slackSection(fmt.Sprintf(
		"*GPU Cost Audit Report*\nAccount: `%s` | Regions: %s",
		result.AccountID, strings.Join(result.Regions, ", "))))

	// Summary
	blocks = append(blocks, slackSection(fmt.Sprintf(
		"*Summary*\n"+
			"• GPU instances: *%d*\n"+
			"• Monthly GPU spend: *$%.0f*\n"+
			"• Estimated waste: *$%.0f* (%.0f%%)",
		s.TotalInstances, s.TotalMonthlyCost,
		s.TotalEstimatedWaste, s.WastePercent)))

	blocks = append(blocks, map[string]any{"type": "divider"})

	// Critical findings
	critical, warning, _ := groupBySeverity(result.Instances)

	if len(critical) > 0 {
		lines := []string{fmt.Sprintf(":red_circle: *CRITICAL — %d instance(s)*", len(critical))}
		for _, inst := range critical {
			name := inst.Name
			if name == "" {
				name = inst.InstanceID
			}
			rec := ""
			if len(inst.Recommendations) > 0 {
				rec = inst.Recommendations[0].Description
			}
			lines = append(lines, fmt.Sprintf("• `%s` (%s) — $%.0f/mo → %s", name, inst.InstanceType, inst.MonthlyCost, rec))
		}
		blocks = append(blocks, slackSection(strings.Join(lines, "\n")))
	}

	if len(warning) > 0 {
		lines := []string{fmt.Sprintf(":warning: *WARNING — %d instance(s)*", len(warning))}
		for _, inst := range warning {
			name := inst.Name
			if name == "" {
				name = inst.InstanceID
			}
			rec := ""
			if len(inst.Recommendations) > 0 {
				rec = inst.Recommendations[0].Description
			}
			lines = append(lines, fmt.Sprintf("• `%s` (%s) — $%.0f/mo → %s", name, inst.InstanceType, inst.MonthlyCost, rec))
		}
		blocks = append(blocks, slackSection(strings.Join(lines, "\n")))
	}

	payload := map[string]any{"blocks": blocks}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func slackSection(text string) map[string]any {
	return map[string]any{
		"type": "section",
		"text": map[string]any{
			"type": "mrkdwn",
			"text": text,
		},
	}
}
