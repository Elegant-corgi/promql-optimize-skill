package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestMissingBaseURL(t *testing.T) {
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", "")
	var stdout bytes.Buffer

	code := run([]string{"-query", "up"}, &stdout, &bytes.Buffer{}, http.DefaultTransport)

	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stdout.String(), "PROMQL_OPTIMIZE_BASE_URL is required") {
		t.Fatalf("expected missing env error, got %s", stdout.String())
	}
}

func TestQueryModeReturnsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("query"); got != "up" {
			t.Fatalf("unexpected query %q", got)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"prometheus"},"value":[1,"1"]}]}}`))
	}))
	defer server.Close()
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", server.URL)

	var stdout bytes.Buffer
	code := run([]string{"-query", "up"}, &stdout, &bytes.Buffer{}, http.DefaultTransport)

	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stdout.String())
	}
	var result probeResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("unexpected status %s", result.Status)
	}
	if result.Evidence["endpoint"] != "/api/v1/query" {
		t.Fatalf("unexpected evidence: %#v", result.Evidence)
	}
}

func TestAuthIsSentButNotPrinted(t *testing.T) {
	const secret = "super-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Fatalf("authorization header not sent")
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", server.URL)
	t.Setenv("PROMQL_OPTIMIZE_TOKEN", secret)
	t.Setenv("PROMQL_OPTIMIZE_HEADERS", `{"X-Scope":"internal"}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"-query", "up"}, &stdout, &stderr, http.DefaultTransport)

	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stdout.String())
	}
	allOutput := stdout.String() + stderr.String()
	if strings.Contains(allOutput, secret) || strings.Contains(allOutput, "X-Scope") {
		t.Fatalf("sensitive data leaked: %s", allOutput)
	}
}

func TestRangeSafetyLimitBlocksRequest(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer server.Close()
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", server.URL)
	t.Setenv("PROMQL_OPTIMIZE_MAX_RANGE", "1h")

	var stdout bytes.Buffer
	code := run([]string{
		"-mode", "range",
		"-query", "up",
		"-start", "2026-05-22T00:00:00Z",
		"-end", "2026-05-22T02:00:00Z",
	}, &stdout, &bytes.Buffer{}, http.DefaultTransport)

	if code == 0 {
		t.Fatal("expected safety failure")
	}
	if called {
		t.Fatal("request should not be sent when range exceeds limit")
	}
	if !strings.Contains(stdout.String(), "exceeds safety limit") {
		t.Fatalf("expected safety message, got %s", stdout.String())
	}
}

func TestSeriesMatcherLimit(t *testing.T) {
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", "http://127.0.0.1:9090")
	t.Setenv("PROMQL_OPTIMIZE_MAX_SERIES_MATCHERS", "1")
	var stdout bytes.Buffer

	code := run([]string{"-mode", "series", "-matchers", `up{job="a"},process_cpu_seconds_total{job="b"}`}, &stdout, &bytes.Buffer{}, http.DefaultTransport)

	if code == 0 {
		t.Fatal("expected matcher limit failure")
	}
	if !strings.Contains(stdout.String(), "exceeds safety limit") {
		t.Fatalf("expected matcher limit message, got %s", stdout.String())
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
