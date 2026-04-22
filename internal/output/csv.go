// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"fmt"
	"io"

	"github.com/gocarina/gocsv"
	"github.com/gpuaudit/cli/internal/models"
)

// FormatCSV marshals the scan instances as CSV.
func FormatCSV(w io.Writer, result *models.ScanResult) error {
	if err := gocsv.Marshal(result.Instances, w); err != nil {
		return fmt.Errorf("encoding CSV: %w", err)
	}
	return nil
}
