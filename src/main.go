// Package main is the entrypoint for the sync-sonatype-scanning-status tool.
//
// It finds application versions that the scanning compliance report flags as
// having a stale Sonatype scan, asks Sonatype whether they were in fact
// scanned (release stage) in the last 90 days, and writes the fresh scan date
// back into the raw FIG Version Scans file so the next
// process-scanning-data run reports them correctly.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kevindowdy/golang-template/src/internal/spreadsheet"
	"github.com/kevindowdy/golang-template/src/utilities/sonatype"
)

const (
	// recentScanWindowDays is how many days back a Sonatype scan still counts as recent.
	recentScanWindowDays = 90
	// releaseStage is the only Sonatype stage that counts as a scan for compliance.
	releaseStage = "release"
)

// config holds every runtime setting for the tool.
type config struct {
	// complianceFile is the "Scanning and Compliance" workbook generated today.
	complianceFile string
	// complianceSheet optionally names the worksheet in complianceFile.
	complianceSheet string
	// scansFile is the raw FIG Version Scans file to update.
	scansFile string
	// scansSheet optionally names the worksheet in scansFile (.xlsx only).
	scansSheet string
	// dryRun reports what would change without writing scansFile.
	dryRun bool
	// client is the configured Sonatype API client.
	client sonatype.Client
}

// envOr returns the environment variable key, or fallback when it is unset or empty.
func envOr(key, fallback string) string {
	// Read the variable.
	if v := os.Getenv(key); v != "" {
		// Use the environment value.
		return v
	}
	// Otherwise use the fallback.
	return fallback
}

// parseConfig builds a config from args, falling back to environment variables.
//
// Environment: SONATYPE_URL, SONATYPE_COOKIE, SONATYPE_USERNAME,
// SONATYPE_PASSWORD, COMPLIANCE_FILE, SCANS_FILE.
func parseConfig(args []string) (config, error) {
	// Start from an empty config.
	var cfg config
	// Define flags on a private FlagSet so tests can call this repeatedly.
	fs := flag.NewFlagSet("sync-sonatype-scanning-status", flag.ContinueOnError)
	// File flags default to the environment.
	fs.StringVar(&cfg.complianceFile, "compliance-file", envOr("COMPLIANCE_FILE", ""), "scanning & compliance workbook generated today (.xlsx or .csv)")
	fs.StringVar(&cfg.complianceSheet, "compliance-sheet", "", "worksheet in the compliance workbook (default: first)")
	fs.StringVar(&cfg.scansFile, "scans-file", envOr("SCANS_FILE", ""), "raw FIG Version Scans file to update (.csv or .xlsx)")
	fs.StringVar(&cfg.scansSheet, "scans-sheet", "", "worksheet in the scans file when it is .xlsx (default: first)")
	// Sonatype endpoint flag defaults to the environment.
	fs.StringVar(&cfg.client.Endpoint, "sonatype-url", envOr("SONATYPE_URL", ""), "Sonatype reports endpoint to GET")
	// Dry run toggle.
	fs.BoolVar(&cfg.dryRun, "dry-run", false, "report what would be updated without writing the scans file")
	// Parse the arguments.
	if err := fs.Parse(args); err != nil {
		// Let the caller print the usage error.
		return cfg, err
	}
	// Credentials come only from the environment so they never land in shell history.
	cfg.client.Cookie = os.Getenv("SONATYPE_COOKIE")
	cfg.client.Username = os.Getenv("SONATYPE_USERNAME")
	cfg.client.Password = os.Getenv("SONATYPE_PASSWORD")
	// Collect every missing required setting for one clear message.
	var missing []string
	if cfg.complianceFile == "" {
		// Record the missing compliance file.
		missing = append(missing, "-compliance-file / COMPLIANCE_FILE")
	}
	if cfg.scansFile == "" {
		// Record the missing scans file.
		missing = append(missing, "-scans-file / SCANS_FILE")
	}
	if cfg.client.Endpoint == "" {
		// Record the missing endpoint.
		missing = append(missing, "-sonatype-url / SONATYPE_URL")
	}
	if len(missing) > 0 {
		// Fail fast listing what to set.
		return cfg, fmt.Errorf("missing required settings: %s", strings.Join(missing, ", "))
	}
	// Configuration is complete.
	return cfg, nil
}

// splitApmVersion splits a "<APM> <version>" key at its first space.
// APM numbers never contain spaces, but versions may.
func splitApmVersion(key string) (apm, version string) {
	// Split into at most two parts.
	parts := strings.SplitN(strings.TrimSpace(key), " ", 2)
	// A key without a space has no version.
	if len(parts) < 2 {
		// Return the APM only.
		return parts[0], ""
	}
	// Return both parts, trimming the version.
	return parts[0], strings.TrimSpace(parts[1])
}

// isIdentChar reports whether b would extend an alphanumeric token.
func isIdentChar(b byte) bool {
	// Letters, digits, and a dot (as in "v1.2") continue a version token.
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '.'
}

// urlMentionsApmVersion reports whether a report URL contains apmVersion,
// matching case-insensitively, in raw or URL-encoded form, and only at token
// boundaries so "APM1 v1" does not match "APM1 v10" or "APM1 v1.2".
func urlMentionsApmVersion(reportURL, apmVersion string) bool {
	// Lower-case the haystack once.
	haystack := strings.ToLower(reportURL)
	// Candidate spellings of the needle as they might appear in a URL.
	needle := strings.ToLower(apmVersion)
	candidates := []string{
		// Raw form.
		needle,
		// Path-escaped form (space becomes %20).
		strings.ToLower(url.PathEscape(apmVersion)),
		// Query-escaped form (space becomes +).
		strings.ToLower(url.QueryEscape(apmVersion)),
	}
	// Check every candidate spelling.
	for _, c := range candidates {
		// Scan all occurrences, not just the first.
		for from := 0; from < len(haystack); {
			// Find the next occurrence at or after from.
			i := strings.Index(haystack[from:], c)
			if i < 0 {
				// No more occurrences of this candidate.
				break
			}
			// Absolute start and end of this occurrence.
			start, end := from+i, from+i+len(c)
			// Both sides must be token boundaries.
			if (start == 0 || !isIdentChar(haystack[start-1])) && (end == len(haystack) || !isIdentChar(haystack[end])) {
				// Genuine match.
				return true
			}
			// Continue after this occurrence.
			from = start + 1
		}
	}
	// No candidate matched on token boundaries.
	return false
}

// findRecentlyScanned applies the scan-check logic: for each APM version, a
// Sonatype result counts when its stage is "release", its URL mentions the APM
// version, and its evaluation date is within the last 90 days of now. It
// returns the recently scanned APM versions (in input order, one entry each)
// and, for each, the most recent qualifying evaluation date.
func findRecentlyScanned(apmVersionsToCheck []string, scanResults []sonatype.ScanResult, now time.Time) ([]string, map[string]time.Time) {
	// Output list of recently scanned versions.
	recentlyScannedApmVersions := []string{}
	// Latest qualifying evaluation date per version.
	latest := make(map[string]time.Time)
	// Check each version we care about.
	for _, apmVersion := range apmVersionsToCheck {
		// Compare against every Sonatype result.
		for _, scanResult := range scanResults {
			// Only release-stage evaluations count as a compliance scan.
			if !strings.EqualFold(strings.TrimSpace(scanResult.Stage), releaseStage) {
				// Wrong stage.
				continue
			}
			// The report URL must refer to this APM version.
			if !urlMentionsApmVersion(scanResult.URL, apmVersion) {
				// Different application version.
				continue
			}
			// Convert the evaluation date to a time.
			evaluated, err := scanResult.Time()
			if err != nil {
				// An unparseable date cannot prove a recent scan.
				slog.Warn("skipping scan result with unparseable date", "url", scanResult.URL, "error", err)
				continue
			}
			// A scan older than 90 days is stale.
			if evaluated.AddDate(0, 0, recentScanWindowDays).Before(now) {
				// Too old to count.
				continue
			}
			// Keep the newest qualifying date for this version.
			if prev, seen := latest[apmVersion]; !seen {
				// First qualifying scan: also record the version once.
				recentlyScannedApmVersions = append(recentlyScannedApmVersions, apmVersion)
				latest[apmVersion] = evaluated
			} else if evaluated.After(prev) {
				// A newer scan replaces the earlier date.
				latest[apmVersion] = evaluated
			}
		}
	}
	// Return the list and the dates.
	return recentlyScannedApmVersions, latest
}

// run executes the whole sync and returns the first fatal error.
func run(ctx context.Context, cfg config, now time.Time) error {
	// Load the stale-Sonatype APM versions from today's compliance workbook.
	apmVersionsToCheck, err := spreadsheet.ReadStaleSonatypeApmVersions(cfg.complianceFile, cfg.complianceSheet)
	if err != nil {
		// Wrap the failure.
		return fmt.Errorf("loading apm versions to check: %w", err)
	}
	// Log how many candidates there are.
	slog.Info("loaded apm versions to check", "count", len(apmVersionsToCheck))
	// Nothing to check means nothing to sync.
	if len(apmVersionsToCheck) == 0 {
		// Exit cleanly.
		return nil
	}
	// Fetch the Sonatype scan results.
	scanResults, err := cfg.client.FetchScanResults(ctx)
	if err != nil {
		// Wrap the failure.
		return fmt.Errorf("fetching sonatype scan results: %w", err)
	}
	// Log how many results came back.
	slog.Info("fetched sonatype scan results", "count", len(scanResults))
	// Decide which versions were actually scanned recently.
	recentlyScannedApmVersions, latest := findRecentlyScanned(apmVersionsToCheck, scanResults, now)
	// Log the outcome.
	slog.Info("recently scanned apm versions found", "count", len(recentlyScannedApmVersions))
	// Build the spreadsheet updates.
	updates := make([]spreadsheet.ScanDateUpdate, 0, len(recentlyScannedApmVersions))
	for _, apmVersion := range recentlyScannedApmVersions {
		// Split the key back into APM and version.
		apm, version := splitApmVersion(apmVersion)
		// Queue the update with the newest scan date.
		updates = append(updates, spreadsheet.ScanDateUpdate{APM: apm, Version: version, Date: latest[apmVersion]})
	}
	// Dry run stops before touching the file.
	if cfg.dryRun {
		// Show what would change.
		for _, u := range updates {
			// One line per pending update.
			slog.Info("dry run: would update", "apm", u.APM, "version", u.Version, "date", u.Date.Format("2006-01-02"))
		}
		// Done without writing.
		return nil
	}
	// Write the dates into the raw scans file.
	result, err := spreadsheet.UpdateSonatypeScanDates(cfg.scansFile, cfg.scansSheet, updates)
	if err != nil {
		// Wrap the failure.
		return fmt.Errorf("updating scans file: %w", err)
	}
	// Warn about versions the raw file did not contain.
	for _, key := range result.Unmatched {
		// One warning per unmatched version.
		slog.Warn("no matching row in scans file", "apm_version", key)
	}
	// Log the summary.
	slog.Info("sync complete", "rows_updated", result.RowsUpdated, "unmatched", len(result.Unmatched))
	// Success.
	return nil
}

// main parses configuration and runs the sync.
func main() {
	// Parse flags and environment.
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		// -h is not an error.
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		// Print the configuration problem.
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	// Run the sync with the current time.
	if err := run(context.Background(), cfg, time.Now()); err != nil {
		// Report the failure and signal it to the caller (scheduler/CI).
		slog.Error("sync failed", "error", err)
		os.Exit(1)
	}
}
