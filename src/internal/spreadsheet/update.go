package spreadsheet

import (
	"fmt"
	"strings"
	"time"
)

// scanDateFormat is how dates are written into the scan date column.
const scanDateFormat = "2006-01-02"

// ScanDateUpdate asks for the Sonatype scan date of one APM version to be set.
type ScanDateUpdate struct {
	// APM is the APM number (AMAPM_Number), e.g. "APM0001234".
	APM string
	// Version is the release/version (Release/Version), e.g. "v1.2".
	Version string
	// Date is the latest Sonatype release-stage evaluation date.
	Date time.Time
}

// UpdateResult summarizes an UpdateSonatypeScanDates call.
type UpdateResult struct {
	// RowsUpdated counts spreadsheet rows written (an APM version may span several rows).
	RowsUpdated int
	// Unmatched lists "<APM> <version>" keys that matched no row.
	Unmatched []string
}

// updateKey builds the case-insensitive lookup key for an APM/version pair.
func updateKey(apm, version string) string {
	// Trim and lower-case both parts, joined by a separator that cannot occur in either.
	return strings.ToLower(strings.TrimSpace(apm)) + "\x00" + strings.ToLower(strings.TrimSpace(version))
}

// UpdateSonatypeScanDates opens the raw FIG Version Scans file and, for every
// row whose AMAPM_Number and Release/Version match an update, sets the
// Last_Sonatype_Scan_Dt column to that update's date (formatted YYYY-MM-DD).
// The file is rewritten atomically only when at least one row changed.
// sheetName selects the worksheet for .xlsx files; empty means the first sheet.
func UpdateSonatypeScanDates(path, sheetName string, updates []ScanDateUpdate) (UpdateResult, error) {
	// Result accumulator.
	var result UpdateResult
	// Nothing to do without updates; avoid touching the file.
	if len(updates) == 0 {
		// Return the zero result.
		return result, nil
	}
	// Load the file as a table.
	t, err := openTable(path, sheetName)
	if err != nil {
		// Surface the load failure.
		return result, err
	}
	// Release the file when done.
	defer t.close()
	// A file without a header row cannot be updated.
	rows := t.rows()
	if len(rows) == 0 {
		// Report the empty file.
		return result, fmt.Errorf("%s is empty", path)
	}
	// Index the header row.
	hdr := newHeaderIndex(rows[0])
	// Resolve the three columns involved.
	apmCol, err := hdr.require(APMHeader)
	if err != nil {
		// Wrap with the file name.
		return result, fmt.Errorf("%s: %w", path, err)
	}
	versionCol, err := hdr.require(VersionHeader)
	if err != nil {
		// Wrap with the file name.
		return result, fmt.Errorf("%s: %w", path, err)
	}
	dateCol, err := hdr.require(SonatypeScanDateHeader)
	if err != nil {
		// Wrap with the file name.
		return result, fmt.Errorf("%s: %w", path, err)
	}
	// Index updates by key; a later duplicate update replaces an earlier one.
	byKey := make(map[string]ScanDateUpdate, len(updates))
	for _, u := range updates {
		// Store the update under its key.
		byKey[updateKey(u.APM, u.Version)] = u
	}
	// Track which keys matched at least one row.
	matched := make(map[string]bool, len(byKey))
	// Walk the data rows (skip the header).
	for i := 1; i < len(rows); i++ {
		// Build this row's key.
		key := updateKey(cell(rows[i], apmCol), cell(rows[i], versionCol))
		// Look for an update for this row.
		u, ok := byKey[key]
		if !ok {
			// Row is not being synced.
			continue
		}
		// Write the new date into the Sonatype scan date column.
		if err := t.setCell(i, dateCol, u.Date.Format(scanDateFormat)); err != nil {
			// Wrap with the row number (1-based, as seen in Excel).
			return result, fmt.Errorf("%s row %d: %w", path, i+1, err)
		}
		// Count the row and mark the key matched.
		result.RowsUpdated++
		matched[key] = true
	}
	// Report updates that matched no row, in input order.
	for _, u := range updates {
		// Only report each unmatched key once.
		if k := updateKey(u.APM, u.Version); !matched[k] {
			// Record the human-readable key.
			result.Unmatched = append(result.Unmatched, strings.TrimSpace(u.APM+" "+u.Version))
			// Prevent duplicates from the same key.
			matched[k] = true
		}
	}
	// Skip the rewrite when nothing changed.
	if result.RowsUpdated == 0 {
		// Return without saving.
		return result, nil
	}
	// Persist the changes.
	if err := t.save(); err != nil {
		// Wrap with the file name.
		return result, fmt.Errorf("saving %s: %w", path, err)
	}
	// Done.
	return result, nil
}
