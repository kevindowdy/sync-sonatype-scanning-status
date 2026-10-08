package spreadsheet

import (
	"fmt"
	"strings"
)

// Header names used by the FIG scanning workbooks. They match the headers
// written by the process-scanning-data tool.
const (
	// ZVXHeader is the combined "<APM> <version>" key column.
	ZVXHeader = "zvx"
	// APMHeader is the APM number column.
	APMHeader = "AMAPM_Number"
	// VersionHeader is the release/version column.
	VersionHeader = "Release/Version"
	// ExtScanningHeader is the overall external-scanning compliance column.
	ExtScanningHeader = "Ext Scanning"
	// FortifyStatusHeader is the Fortify scanning status column.
	FortifyStatusHeader = "Fortify_Scanning_Status"
	// WebInspectStatusHeader is the WebInspect scanning status column.
	WebInspectStatusHeader = "WebInspect_Scanning_Status"
	// FortifyAgeHeader is the days-since-last-Fortify-scan column.
	FortifyAgeHeader = "FOP AGE"
	// WebInspectAgeHeader is the days-since-last-WebInspect-scan column.
	WebInspectAgeHeader = "WI AGE"
	// SonatypeScanDateHeader is the last Sonatype scan date column in the raw export.
	SonatypeScanDateHeader = "Last_Sonatype_Scan_Dt"
)

// sonatypeAgeHeaders are the accepted names of the days-since-last-Sonatype-scan column.
var sonatypeAgeHeaders = []string{"SNT Age", "SN Age"}

// headerIndex maps normalized header names to their zero-based column index.
type headerIndex map[string]int

// normalizeHeader lower-cases and trims a header for tolerant matching.
func normalizeHeader(h string) string {
	// Trim whitespace and lower-case.
	return strings.ToLower(strings.TrimSpace(h))
}

// newHeaderIndex indexes a header row; the first occurrence of a name wins.
func newHeaderIndex(header []string) headerIndex {
	// Allocate the index.
	idx := make(headerIndex, len(header))
	// Walk the header cells in order.
	for i, h := range header {
		// Normalize the cell text.
		key := normalizeHeader(h)
		// Keep only the first occurrence.
		if _, exists := idx[key]; !exists && key != "" {
			// Record the column position.
			idx[key] = i
		}
	}
	// Return the finished index.
	return idx
}

// find returns the column of the first name present, or -1 when none is.
func (h headerIndex) find(names ...string) int {
	// Try each candidate name in order.
	for _, name := range names {
		// Look up the normalized candidate.
		if i, ok := h[normalizeHeader(name)]; ok {
			// Found it.
			return i
		}
	}
	// None of the names exist.
	return -1
}

// require is find that fails with a descriptive error when no name exists.
func (h headerIndex) require(names ...string) (int, error) {
	// Look for the column.
	i := h.find(names...)
	if i < 0 {
		// Name the missing column(s) to make the failure actionable.
		return 0, fmt.Errorf("missing required column %q", strings.Join(names, "\" or \""))
	}
	// Return the column index.
	return i, nil
}

// cell returns the trimmed cell at col, or "" when the row is shorter or col < 0.
func cell(row []string, col int) string {
	// Out-of-range or absent columns read as empty.
	if col < 0 || col >= len(row) {
		// Empty value.
		return ""
	}
	// Trim padding whitespace.
	return strings.TrimSpace(row[col])
}
