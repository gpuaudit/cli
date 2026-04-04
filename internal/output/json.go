package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/maksimov/gpuaudit/internal/models"
)

// FormatJSON writes the scan result as pretty-printed JSON.
func FormatJSON(w io.Writer, result *models.ScanResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	return nil
}
