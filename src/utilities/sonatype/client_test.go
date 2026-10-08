package sonatype

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFetchScanResultsCookieAuth verifies cookie auth wins and JSON decodes.
func TestFetchScanResultsCookieAuth(t *testing.T) {
	// Server asserting the headers and returning one result.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cookie must be forwarded verbatim.
		if r.Header.Get("Cookie") != "CLM-CSRF-TOKEN=abc; JSESSIONID=xyz" {
			t.Errorf("cookie = %q", r.Header.Get("Cookie"))
		}
		// Basic auth must not be sent when a cookie exists.
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("basic auth sent alongside cookie")
		}
		// JSON must be requested.
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("accept = %q", r.Header.Get("Accept"))
		}
		// Respond with a sample payload.
		w.Write([]byte(`[{"applicationId":"a","stage":"release","evaluationDate":"2026-09-01T10:00:00.123-04:00","reportHtmlUrl":"https://x/APM1 v1/r"}]`))
	}))
	defer srv.Close()
	// Fetch with a cookie.
	c := Client{Endpoint: srv.URL, Cookie: "CLM-CSRF-TOKEN=abc; JSESSIONID=xyz", Username: "u", Password: "p"}
	got, err := c.FetchScanResults(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// One result with the expected fields.
	if len(got) != 1 || got[0].Stage != "release" || got[0].URL != "https://x/APM1 v1/r" {
		t.Fatalf("got %+v", got)
	}
}

// TestFetchScanResultsBasicAuth verifies the username/password fallback.
func TestFetchScanResultsBasicAuth(t *testing.T) {
	// Server requiring basic auth.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check the credentials.
		if u, p, ok := r.BasicAuth(); !ok || u != "u" || p != "p" {
			t.Errorf("basic auth = %v %q %q", ok, u, p)
		}
		// Empty array is valid.
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	// Fetch with username/password only.
	c := Client{Endpoint: srv.URL, Username: "u", Password: "p"}
	if _, err := c.FetchScanResults(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestFetchScanResultsErrors covers config, status, and decode failures.
func TestFetchScanResultsErrors(t *testing.T) {
	// Server that returns 401 on /deny and an HTML page on /html.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Route by path.
		if r.URL.Path == "/deny" {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		// Login page masquerading as 200 OK.
		w.Write([]byte("<html>login</html>"))
	}))
	defer srv.Close()
	// Table of failing clients and the error text expected.
	tests := []struct {
		name string
		c    Client
		want string
	}{
		{"no endpoint", Client{Cookie: "c"}, "not configured"},
		{"no credentials", Client{Endpoint: srv.URL}, "no sonatype credentials"},
		{"unauthorized", Client{Endpoint: srv.URL + "/deny", Cookie: "c"}, "401"},
		{"html instead of json", Client{Endpoint: srv.URL + "/html", Cookie: "c"}, "decoding"},
	}
	// Run each case.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each must fail with the expected message.
			_, err := tt.c.FetchScanResults(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

// TestFetchScanResultsDoesNotLeakCredentials ensures secrets stay out of errors.
func TestFetchScanResultsDoesNotLeakCredentials(t *testing.T) {
	// Server that always fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusForbidden)
	}))
	defer srv.Close()
	// Fetch with a secret cookie.
	_, err := (&Client{Endpoint: srv.URL, Cookie: "SECRET-COOKIE"}).FetchScanResults(context.Background())
	// The error must not contain it.
	if err == nil || strings.Contains(err.Error(), "SECRET-COOKIE") {
		t.Fatalf("err = %v", err)
	}
}

// TestScanResultTime checks the supported timestamp layouts.
func TestScanResultTime(t *testing.T) {
	// Layout samples that must parse.
	for _, s := range []string{"2026-09-01T10:00:00.123-04:00", "2026-09-01T10:00:00Z", "2026-09-01T10:00:00.000+0000", "2026-09-01"} {
		// Parse each sample.
		if _, err := (ScanResult{EvaluationDate: s}).Time(); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	// Garbage must error.
	if _, err := (ScanResult{EvaluationDate: "yesterday"}).Time(); err == nil {
		t.Error("expected error")
	}
}
