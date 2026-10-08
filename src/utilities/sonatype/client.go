// Package sonatype standardizes the contract with the Sonatype IQ Server
// reports API used to find out when an application version was last scanned.
package sonatype

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxErrorBodyBytes caps how much of an error response is echoed into errors.
const maxErrorBodyBytes = 200

// evaluationDateLayouts lists the timestamp formats Sonatype has been seen to emit.
var evaluationDateLayouts = []string{
	// Standard RFC 3339 with optional fractional seconds, e.g. 2026-09-01T10:00:00.123-04:00.
	time.RFC3339Nano,
	// Offset without a colon, e.g. 2026-09-01T10:00:00.123+0000.
	"2006-01-02T15:04:05.999999999Z0700",
	// Plain date only.
	"2006-01-02",
}

// ScanResult is one Sonatype evaluation (scan) report entry.
type ScanResult struct {
	// ApplicationID is the Sonatype application identifier.
	ApplicationID string `json:"applicationId"`
	// Stage is the pipeline stage the evaluation ran in (e.g. "build", "release").
	Stage string `json:"stage"`
	// EvaluationDate is the raw timestamp of the evaluation; use Time to parse it.
	EvaluationDate string `json:"evaluationDate"`
	// URL is the link to the HTML evaluation report; it embeds the APM and version.
	URL string `json:"reportHtmlUrl"`
}

// Time parses EvaluationDate, trying every known Sonatype timestamp layout.
func (r ScanResult) Time() (time.Time, error) {
	// Try each layout in order and return the first one that parses.
	for _, layout := range evaluationDateLayouts {
		// Attempt the parse with the current layout.
		if t, err := time.Parse(layout, r.EvaluationDate); err == nil {
			// Return the successfully parsed time.
			return t, nil
		}
	}
	// No layout matched, so report the offending value.
	return time.Time{}, fmt.Errorf("unrecognized evaluationDate %q", r.EvaluationDate)
}

// Client talks to the Sonatype IQ Server reports endpoint.
type Client struct {
	// Endpoint is the full URL of the reports API (GET returns a JSON array).
	Endpoint string
	// Cookie is an optional raw Cookie header value; it takes precedence over basic auth.
	Cookie string
	// Username is the basic-auth user, used only when Cookie is empty.
	Username string
	// Password is the basic-auth password, used only when Cookie is empty.
	Password string
	// HTTPClient performs the request; http.DefaultClient (with a timeout) when nil.
	HTTPClient *http.Client
}

// FetchScanResults calls the reports endpoint and decodes the scan results.
//
// Authentication: if Cookie is set it is sent as the Cookie header, otherwise
// Username/Password are sent as HTTP basic auth. Credentials are never logged
// or included in returned errors.
func (c *Client) FetchScanResults(ctx context.Context) ([]ScanResult, error) {
	// Fail fast when no endpoint is configured.
	if c.Endpoint == "" {
		// Report the missing configuration.
		return nil, errors.New("sonatype endpoint is not configured")
	}
	// Build the GET request bound to the caller's context.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint, nil)
	if err != nil {
		// Wrap the construction failure.
		return nil, fmt.Errorf("building sonatype request: %w", err)
	}
	// Ask for JSON so the server does not answer with HTML.
	req.Header.Set("Accept", "application/json")
	// Identify this tool to the server.
	req.Header.Set("User-Agent", "sync-sonatype-scanning-status")
	// Prefer cookie auth when a cookie value was supplied.
	if c.Cookie != "" {
		// Send the cookie value verbatim.
		req.Header.Set("Cookie", c.Cookie)
	} else if c.Username != "" || c.Password != "" {
		// Otherwise fall back to HTTP basic auth.
		req.SetBasicAuth(c.Username, c.Password)
	} else {
		// Without any credential the call cannot succeed, so stop early.
		return nil, errors.New("no sonatype credentials configured: set a cookie or username and password")
	}
	// Use the injected client or a default with a sane timeout.
	httpClient := c.HTTPClient
	if httpClient == nil {
		// Default client so a hung server cannot block forever.
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	// Execute the request.
	resp, err := httpClient.Do(req)
	if err != nil {
		// Wrap the transport failure.
		return nil, fmt.Errorf("calling sonatype: %w", err)
	}
	// Always release the connection.
	defer resp.Body.Close()
	// Treat any non-2xx status as a failure.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Read a small prefix of the body to help troubleshooting.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		// Return the status with the snippet.
		return nil, fmt.Errorf("sonatype returned %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
	}
	// Decode the JSON array of results.
	var results []ScanResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		// An HTML login page here usually means the cookie expired.
		return nil, fmt.Errorf("decoding sonatype response (expired cookie or wrong endpoint?): %w", err)
	}
	// Hand back the decoded results.
	return results, nil
}
