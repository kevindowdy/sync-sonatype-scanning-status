package spreadsheet

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// complianceRows is a compliance workbook fixture covering every filter.
var complianceRows = [][]string{
	{"zvx", "AMAPM_Number", "Release/Version", "FOP AGE", "WI AGE", "SNT Age", "Fortify_Scanning_Status", "WebInspect_Scanning_Status", "Ext Scanning"},
	// Kept: non compliant, only Sonatype stale.
	{"APM1 v1", "APM1", "v1", "10", "20", "120", "Scanned", "Scanned", "Non Compliant"},
	// Dropped: compliant overall.
	{"APM2 v1", "APM2", "v1", "10", "20", "120", "Scanned", "Scanned", "Compliant"},
	// Dropped: Sonatype age under 90.
	{"APM3 v1", "APM3", "v1", "10", "20", "30", "Scanned", "Scanned", "Non Compliant"},
	// Dropped: Fortify stale.
	{"APM4 v1", "APM4", "v1", "200", "20", "120", "Scanned", "Scanned", "Non Compliant"},
	// Dropped: Fortify out of scope.
	{"APM5 v1", "APM5", "v1", "10", "20", "120", "Out Of Scope - x", "Scanned", "Non Compliant"},
	// Dropped: WebInspect stale.
	{"APM6 v1", "APM6", "v1", "10", "200", "120", "Scanned", "Scanned", "Non Compliant"},
	// Dropped: WebInspect out of scope (case variant).
	{"APM7 v1", "APM7", "v1", "10", "20", "120", "Scanned", "Out of Scope", "Non Compliant"},
	// Kept: spelling variant, float age, blank zvx falls back to APM + version.
	{"", "APM8", "v 2", "10.0", "20.0", "120.0", "Scanned", "Scanned", "Non-compliant"},
	// Kept: Sonatype never scanned (blank age).
	{"APM9 v1", "APM9", "v1", "10", "20", "", "Scanned", "Scanned", "Non Compliant"},
	// Dropped: duplicate of the first kept row.
	{"APM1 v1", "APM1", "v1", "10", "20", "120", "Scanned", "Scanned", "Non Compliant"},
}

// writeXLSX writes rows to a new workbook and returns its path.
func writeXLSX(t *testing.T, rows [][]string) string {
	t.Helper()
	// New workbook in a temp dir.
	path := filepath.Join(t.TempDir(), "book.xlsx")
	f := excelize.NewFile()
	// Put each row on the default sheet.
	for r, row := range rows {
		for c, v := range row {
			name, _ := excelize.CoordinatesToCellName(c+1, r+1)
			f.SetCellValue("Sheet1", name, v)
		}
	}
	// Save and close.
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

// TestReadStaleSonatypeApmVersionsXLSX exercises every filter on an xlsx file.
func TestReadStaleSonatypeApmVersionsXLSX(t *testing.T) {
	// Run the filter.
	got, err := ReadStaleSonatypeApmVersions(writeXLSX(t, complianceRows), "")
	if err != nil {
		t.Fatal(err)
	}
	// Expected survivors in file order.
	want := []string{"APM1 v1", "APM8 v 2", "APM9 v1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestReadStaleSonatypeApmVersionsErrors covers bad files and headers.
func TestReadStaleSonatypeApmVersionsErrors(t *testing.T) {
	dir := t.TempDir()
	// Unsupported extension.
	if _, err := ReadStaleSonatypeApmVersions(filepath.Join(dir, "a.txt"), ""); err == nil {
		t.Error("expected unsupported type error")
	}
	// Missing file.
	if _, err := ReadStaleSonatypeApmVersions(filepath.Join(dir, "none.xlsx"), ""); err == nil {
		t.Error("expected open error")
	}
	// Missing Ext Scanning column.
	if _, err := ReadStaleSonatypeApmVersions(writeXLSX(t, [][]string{{"zvx", "SNT Age"}}), ""); err == nil {
		t.Error("expected missing column error")
	}
	// Missing key columns.
	if _, err := ReadStaleSonatypeApmVersions(writeXLSX(t, [][]string{{"Ext Scanning", "SNT Age"}}), ""); err == nil {
		t.Error("expected missing key columns error")
	}
}

// TestSNAgeAlias confirms the "SN Age" spelling is accepted.
func TestSNAgeAlias(t *testing.T) {
	// Workbook using the alternate header.
	path := writeXLSX(t, [][]string{{"zvx", "SN Age", "Ext Scanning"}, {"APM1 v1", "100", "Non Compliant"}})
	got, err := ReadStaleSonatypeApmVersions(path, "")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
}

// scansCSV is a raw scans export fixture with BOM and CRLF to prove they survive.
const scansCSV = "\xef\xbb\xbfAMAPM_Number,Release/Version,Last_Sonatype_Scan_Dt\r\n" +
	"APM1,v1,2025-01-01\r\n" +
	"APM1,v2,2025-01-01\r\n" +
	"apm8, v 2 ,\r\n" +
	"APM8,v 2,\r\n"

// TestUpdateSonatypeScanDatesCSV updates a CSV and checks formatting survives.
func TestUpdateSonatypeScanDatesCSV(t *testing.T) {
	// Write the fixture.
	path := filepath.Join(t.TempDir(), "scans.csv")
	if err := os.WriteFile(path, []byte(scansCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	d := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	// Update APM1 v1 and APM8 "v 2" (two rows), plus one unknown version.
	res, err := UpdateSonatypeScanDates(path, "", []ScanDateUpdate{
		{APM: "APM1", Version: "v1", Date: d},
		{APM: "APM8", Version: "v 2", Date: d},
		{APM: "APM404", Version: "v9", Date: d},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Three rows written, one unmatched.
	if res.RowsUpdated != 3 || !reflect.DeepEqual(res.Unmatched, []string{"APM404 v9"}) {
		t.Fatalf("result = %+v", res)
	}
	// Only the targeted rows changed; BOM and CRLF preserved.
	got, _ := os.ReadFile(path)
	want := "\xef\xbb\xbfAMAPM_Number,Release/Version,Last_Sonatype_Scan_Dt\r\n" +
		"APM1,v1,2026-09-01\r\n" +
		"APM1,v2,2025-01-01\r\n" +
		// Go quotes fields with leading spaces; the value itself is unchanged.
		"apm8,\" v 2 \",2026-09-01\r\n" +
		"APM8,v 2,2026-09-01\r\n"
	if string(got) != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// Permissions are preserved.
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

// TestUpdateSonatypeScanDatesXLSX updates a workbook in place.
func TestUpdateSonatypeScanDatesXLSX(t *testing.T) {
	// Workbook with a ragged row (no date cell at all).
	path := writeXLSX(t, [][]string{
		{"AMAPM_Number", "Release/Version", "Last_Sonatype_Scan_Dt"},
		{"APM1", "v1"},
	})
	d := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	res, err := UpdateSonatypeScanDates(path, "Sheet1", []ScanDateUpdate{{APM: "APM1", Version: "v1", Date: d}})
	if err != nil || res.RowsUpdated != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// Reopen and verify the value.
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if v, _ := f.GetCellValue("Sheet1", "C2"); v != "2026-09-01" {
		t.Fatalf("C2 = %q", v)
	}
}

// TestUpdateSonatypeScanDatesNoOp ensures untouched files are not rewritten.
func TestUpdateSonatypeScanDatesNoOp(t *testing.T) {
	// Fixture file.
	path := filepath.Join(t.TempDir(), "scans.csv")
	os.WriteFile(path, []byte(scansCSV), 0o644)
	// Nonexistent version only.
	res, err := UpdateSonatypeScanDates(path, "", []ScanDateUpdate{{APM: "X", Version: "y", Date: time.Now()}})
	if err != nil || res.RowsUpdated != 0 || len(res.Unmatched) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// Empty updates short-circuit even for a missing file.
	if _, err := UpdateSonatypeScanDates(filepath.Join(t.TempDir(), "missing.csv"), "", nil); err != nil {
		t.Fatal(err)
	}
	// Missing date column errors.
	bad := filepath.Join(t.TempDir(), "bad.csv")
	os.WriteFile(bad, []byte("AMAPM_Number,Release/Version\nA,b\n"), 0o644)
	if _, err := UpdateSonatypeScanDates(bad, "", []ScanDateUpdate{{APM: "A", Version: "b", Date: time.Now()}}); err == nil {
		t.Error("expected missing column error")
	}
}
