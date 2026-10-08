package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kevindowdy/golang-template/src/utilities/sonatype"
	"github.com/xuri/excelize/v2"
)

// fixedNow is the clock used by every test.
var fixedNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// TestUrlMentionsApmVersion checks case, encoding, and token boundaries.
func TestUrlMentionsApmVersion(t *testing.T) {
	// Table of URL / needle / expectation.
	tests := []struct {
		url, needle string
		want        bool
	}{
		{"https://iq/ui/APM1 v1/report/1", "APM1 v1", true},
		{"https://iq/ui/apm1%20v1/report/1", "APM1 v1", true},
		{"https://iq/ui?app=APM1+v1&x=1", "APM1 v1", true},
		{"https://iq/ui/APM1 v10/report", "APM1 v1", false},
		{"https://iq/ui/APM1 v1.2/report", "APM1 v1", false},
		{"https://iq/ui/XAPM1 v1/report", "APM1 v1", false},
		{"https://iq/ui/APM1 v10/ and APM1 v1/", "APM1 v1", true},
		{"https://iq/ui/other", "APM1 v1", false},
	}
	// Run each case.
	for _, tt := range tests {
		if got := urlMentionsApmVersion(tt.url, tt.needle); got != tt.want {
			t.Errorf("urlMentionsApmVersion(%q, %q) = %v", tt.url, tt.needle, got)
		}
	}
}

// TestFindRecentlyScanned covers stage, URL, date, staleness and newest-date selection.
func TestFindRecentlyScanned(t *testing.T) {
	// Helper to build a result n days before fixedNow.
	res := func(stage, u string, daysAgo int) sonatype.ScanResult {
		return sonatype.ScanResult{Stage: stage, URL: u, EvaluationDate: fixedNow.AddDate(0, 0, -daysAgo).Format(time.RFC3339)}
	}
	results := []sonatype.ScanResult{
		// APM1 v1: older release scan, then newer one, plus a build scan that must be ignored.
		res("release", "https://iq/APM1 v1/r1", 60),
		res("release", "https://iq/APM1 v1/r2", 5),
		res("build", "https://iq/APM1 v1/r3", 1),
		// APM2 v1: only a stale release scan (91 days).
		res("release", "https://iq/APM2 v1/r", 91),
		// APM3 v1: only a build scan.
		res("build", "https://iq/APM3 v1/r", 2),
		// APM4 v1: exactly 90 days old is still recent.
		res("Release", "https://iq/APM4 v1/r", 90),
		// APM5 v1: unparseable date is ignored.
		{Stage: "release", URL: "https://iq/APM5 v1/r", EvaluationDate: "garbage"},
	}
	// Check all candidates plus one unknown.
	got, latest := findRecentlyScanned([]string{"APM1 v1", "APM2 v1", "APM3 v1", "APM4 v1", "APM5 v1", "APM6 v1"}, results, fixedNow)
	// Only APM1 and APM4 qualify, in input order.
	if want := []string{"APM1 v1", "APM4 v1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// APM1 uses the newest qualifying release date (5 days ago), not the build scan.
	if want := fixedNow.AddDate(0, 0, -5).Truncate(time.Second); !latest["APM1 v1"].Equal(want) {
		t.Fatalf("latest = %v, want %v", latest["APM1 v1"], want)
	}
}

// TestSplitApmVersion checks key splitting, including versions with spaces.
func TestSplitApmVersion(t *testing.T) {
	// Cases.
	for key, want := range map[string][2]string{"APM1 v1": {"APM1", "v1"}, "APM8 v 2": {"APM8", "v 2"}, "APM9": {"APM9", ""}} {
		// Split and compare.
		if a, v := splitApmVersion(key); a != want[0] || v != want[1] {
			t.Errorf("%q -> %q,%q", key, a, v)
		}
	}
}

// TestParseConfig verifies required settings and env fallbacks.
func TestParseConfig(t *testing.T) {
	// Missing everything fails.
	t.Setenv("COMPLIANCE_FILE", "")
	t.Setenv("SCANS_FILE", "")
	t.Setenv("SONATYPE_URL", "")
	if _, err := parseConfig(nil); err == nil {
		t.Fatal("expected missing settings error")
	}
	// Environment satisfies the settings; credentials are read from the environment.
	t.Setenv("COMPLIANCE_FILE", "c.xlsx")
	t.Setenv("SCANS_FILE", "s.csv")
	t.Setenv("SONATYPE_URL", "https://iq/api")
	t.Setenv("SONATYPE_COOKIE", "k=v")
	cfg, err := parseConfig([]string{"-dry-run"})
	if err != nil || cfg.complianceFile != "c.xlsx" || cfg.client.Cookie != "k=v" || !cfg.dryRun {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

// TestRunEndToEnd runs the full sync against a fake Sonatype server and temp files.
func TestRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	// Compliance workbook with two stale-Sonatype rows.
	book := excelize.NewFile()
	rows := [][]string{
		{"zvx", "Ext Scanning", "SNT Age", "FOP AGE", "WI AGE"},
		{"APM1 v1", "Non Compliant", "120", "5", "5"},
		{"APM2 v1", "Non Compliant", "120", "5", "5"},
	}
	for r, row := range rows {
		for c, v := range row {
			name, _ := excelize.CoordinatesToCellName(c+1, r+1)
			book.SetCellValue("Sheet1", name, v)
		}
	}
	compliance := filepath.Join(dir, "compliance.xlsx")
	if err := book.SaveAs(compliance); err != nil {
		t.Fatal(err)
	}
	// Raw scans CSV.
	scans := filepath.Join(dir, "scans.csv")
	os.WriteFile(scans, []byte("AMAPM_Number,Release/Version,Last_Sonatype_Scan_Dt\nAPM1,v1,2025-01-01\nAPM2,v1,2025-01-01\n"), 0o644)
	// Fake Sonatype: APM1 scanned 10 days ago, APM2 only 200 days ago.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return two results.
		w.Write([]byte(`[` +
			`{"stage":"release","evaluationDate":"` + fixedNow.AddDate(0, 0, -10).Format(time.RFC3339) + `","reportHtmlUrl":"https://iq/APM1 v1/r"},` +
			`{"stage":"release","evaluationDate":"` + fixedNow.AddDate(0, 0, -200).Format(time.RFC3339) + `","reportHtmlUrl":"https://iq/APM2 v1/r"}]`))
	}))
	defer srv.Close()
	cfg := config{complianceFile: compliance, scansFile: scans, client: sonatype.Client{Endpoint: srv.URL, Cookie: "c"}}
	// Dry run leaves the file alone.
	cfg.dryRun = true
	if err := run(context.Background(), cfg, fixedNow); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(scans); string(b) != "AMAPM_Number,Release/Version,Last_Sonatype_Scan_Dt\nAPM1,v1,2025-01-01\nAPM2,v1,2025-01-01\n" {
		t.Fatalf("dry run modified file: %q", b)
	}
	// Real run updates only APM1.
	cfg.dryRun = false
	if err := run(context.Background(), cfg, fixedNow); err != nil {
		t.Fatal(err)
	}
	want := "AMAPM_Number,Release/Version,Last_Sonatype_Scan_Dt\nAPM1,v1,2026-09-28\nAPM2,v1,2025-01-01\n"
	if b, _ := os.ReadFile(scans); string(b) != want {
		t.Fatalf("got %q want %q", b, want)
	}
}
