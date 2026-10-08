package spreadsheet

import (
	"fmt"
	"strconv"
	"strings"
)

// staleThresholdDays is the age (in days) above which a scan is considered stale.
const staleThresholdDays = 90

// outOfScopePrefix is the case-insensitive prefix of an "Out of Scope" status.
const outOfScopePrefix = "out of scope"

// isNonCompliant reports whether an Ext Scanning value means "Non Compliant",
// tolerating spelling variants such as "Non-compliant" and "Non Compliant".
func isNonCompliant(value string) bool {
	// Strip everything but letters so spaces and hyphens do not matter.
	var b strings.Builder
	// Walk the characters of the value.
	for _, r := range strings.ToLower(value) {
		// Keep letters only.
		if r >= 'a' && r <= 'z' {
			// Append the letter.
			b.WriteRune(r)
		}
	}
	// Compare to the canonical form.
	return b.String() == "noncompliant"
}

// isOutOfScope reports whether a scanning status starts with "Out of Scope".
func isOutOfScope(status string) bool {
	// Case-insensitive prefix match.
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(status)), outOfScopePrefix)
}

// ageOver parses an age cell and reports whether it exceeds the threshold.
// The second result is false when the cell is blank or not a number (unknown).
func ageOver(value string, threshold float64) (over bool, known bool) {
	// Blank cells mean the scan date is unknown.
	if value == "" {
		// Unknown age.
		return false, false
	}
	// Parse as float since Excel may emit "123.0".
	age, err := strconv.ParseFloat(value, 64)
	if err != nil {
		// Unparseable counts as unknown.
		return false, false
	}
	// Compare to the threshold.
	return age > threshold, true
}

// ReadStaleSonatypeApmVersions opens the "Scanning and Compliance Data
// Generated Today" workbook (all_apm_versions) and returns the APM versions
// whose Sonatype scan is worth re-checking against Sonatype (apm_versions_to_check).
//
// A row is kept when ALL of these hold:
//   - Ext Scanning is "Non Compliant";
//   - the Sonatype age (SNT Age) is greater than 90 days, or unknown (never
//     recorded, which is the most out-of-sync state the sync can repair);
//   - it is NOT stale because of Fortify or WebInspect, i.e. it is not true that
//     (FOP AGE > 90 or Fortify status is Out of Scope) OR
//     (WI AGE > 90 or WebInspect status is Out of Scope).
//
// Each returned value is the row's "zvx" cell ("<APM> <version>"; built from
// AMAPM_Number and Release/Version when blank), de-duplicated in file order.
// sheetName selects the worksheet for .xlsx files; empty means the first sheet.
func ReadStaleSonatypeApmVersions(path, sheetName string) ([]string, error) {
	// Load the file as a table.
	t, err := openTable(path, sheetName)
	if err != nil {
		// Surface the load failure.
		return nil, err
	}
	// Release the file when done.
	defer t.close()
	// A file without a header row cannot be filtered.
	rows := t.rows()
	if len(rows) == 0 {
		// Report the empty file.
		return nil, fmt.Errorf("%s is empty", path)
	}
	// Index the header row by column name.
	hdr := newHeaderIndex(rows[0])
	// Resolve the columns the filters need.
	extCol, err := hdr.require(ExtScanningHeader)
	if err != nil {
		// Wrap with the file name.
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// The Sonatype age column has two accepted spellings.
	sntCol, err := hdr.require(sonatypeAgeHeaders...)
	if err != nil {
		// Wrap with the file name.
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Remaining columns are optional so a partial workbook still filters.
	fopCol := hdr.find(FortifyAgeHeader)
	wiCol := hdr.find(WebInspectAgeHeader)
	fortifyStatusCol := hdr.find(FortifyStatusHeader)
	webInspectStatusCol := hdr.find(WebInspectStatusHeader)
	// The key comes from zvx, falling back to APM + version.
	zvxCol := hdr.find(ZVXHeader)
	apmCol := hdr.find(APMHeader)
	versionCol := hdr.find(VersionHeader)
	// Without zvx we need both APM and version to build a key.
	if zvxCol < 0 && (apmCol < 0 || versionCol < 0) {
		// Report which columns would satisfy the requirement.
		return nil, fmt.Errorf("%s: need a %q column or both %q and %q", path, ZVXHeader, APMHeader, VersionHeader)
	}
	// Track emitted keys to de-duplicate while keeping order.
	seen := make(map[string]struct{})
	// Collect the result.
	var versions []string
	// Walk the data rows (skip the header).
	for _, row := range rows[1:] {
		// Filter 1: only Non Compliant rows.
		if !isNonCompliant(cell(row, extCol)) {
			// Skip compliant / out-of-scope rows.
			continue
		}
		// Filter 2: Sonatype age must be over 90 days; blank means never recorded.
		over, known := ageOver(cell(row, sntCol), staleThresholdDays)
		if known && !over {
			// Recently scanned already, nothing to sync.
			continue
		}
		// Filter 3a: Fortify makes the row stale regardless of Sonatype.
		fopOver, _ := ageOver(cell(row, fopCol), staleThresholdDays)
		fortifyStale := fopOver || isOutOfScope(cell(row, fortifyStatusCol))
		// Filter 3b: WebInspect likewise.
		wiOver, _ := ageOver(cell(row, wiCol), staleThresholdDays)
		webInspectStale := wiOver || isOutOfScope(cell(row, webInspectStatusCol))
		// Drop rows whose staleness cannot be fixed by a Sonatype sync.
		if fortifyStale || webInspectStale {
			// Skip this row.
			continue
		}
		// Prefer the zvx cell as the key.
		key := cell(row, zvxCol)
		if key == "" {
			// Build "<APM> <version>" the same way process-scanning-data does.
			key = strings.TrimSpace(cell(row, apmCol) + " " + cell(row, versionCol))
		}
		// Ignore rows that have no usable key.
		if key == "" {
			// Nothing to look up.
			continue
		}
		// Skip duplicates.
		if _, dup := seen[key]; dup {
			// Already emitted.
			continue
		}
		// Remember and emit the key.
		seen[key] = struct{}{}
		versions = append(versions, key)
	}
	// Return the apm_versions_to_check.
	return versions, nil
}
