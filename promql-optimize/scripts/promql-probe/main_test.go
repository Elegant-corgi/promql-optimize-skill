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
		if got := r.URL.Query().Get("query"); got != `up{job="prometheus"}` {
			t.Fatalf("unexpected query %q", got)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"prometheus"},"value":[1,"1"]}]}}`))
	}))
	defer server.Close()
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", server.URL)

	var stdout bytes.Buffer
	code := run([]string{"-query", `up{job="prometheus"}`}, &stdout, &bytes.Buffer{}, http.DefaultTransport)

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

func TestQueryModeRejectsBackslashEscapedQuotes(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", server.URL)
	t.Setenv("PROMQL_OPTIMIZE_TOKEN", "super-secret-token")
	t.Setenv("PROMQL_OPTIMIZE_HEADERS", `{"X-Scope":"internal"}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"-query", `up{job=\"snmp_exporter\"}`}, &stdout, &stderr, http.DefaultTransport)

	if code == 0 {
		t.Fatal("expected escaping validation failure")
	}
	if called {
		t.Fatal("request should not be sent when query contains backslash-escaped quotes")
	}
	var result probeResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	for _, expected := range []string{
		"backslash-escaped double quotes",
		`up{job="snmp_exporter"}`,
		"PowerShell single-quoted argument",
	} {
		if !strings.Contains(result.Error, expected) {
			t.Fatalf("expected %q in error, got %s", expected, result.Error)
		}
	}
	output := stdout.String() + stderr.String()
	for _, sensitive := range []string{server.URL, "super-secret-token", "X-Scope"} {
		if strings.Contains(output, sensitive) {
			t.Fatalf("sensitive data leaked: %s", output)
		}
	}
}

func TestRangeModeRejectsBackslashEscapedQuotes(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer server.Close()
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", server.URL)

	var stdout bytes.Buffer
	code := run([]string{
		"-mode", "range",
		"-query", `rate(http_requests_total{job=\"api\"}[5m])`,
		"-start", "2026-05-22T00:00:00Z",
		"-end", "2026-05-22T00:30:00Z",
	}, &stdout, &bytes.Buffer{}, http.DefaultTransport)

	if code == 0 {
		t.Fatal("expected escaping validation failure")
	}
	if called {
		t.Fatal("request should not be sent when range query contains backslash-escaped quotes")
	}
	var result probeResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if !strings.Contains(result.Error, `rate(http_requests_total{job="api"}[5m])`) {
		t.Fatalf("expected suggested query, got %s", result.Error)
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

func TestHTTPErrorIncludesSafeAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": parse error"}`))
	}))
	defer server.Close()
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", server.URL)

	var stdout bytes.Buffer
	code := run([]string{"-query", "up{"}, &stdout, &bytes.Buffer{}, http.DefaultTransport)

	if code == 0 {
		t.Fatal("expected API failure")
	}
	var result probeResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	for _, expected := range []string{"HTTP 422", "errorType=bad_data", "parse error"} {
		if !strings.Contains(result.Error, expected) {
			t.Fatalf("expected %q in error, got %s", expected, result.Error)
		}
	}
}

func TestHTTPErrorRedactsSensitiveAPIError(t *testing.T) {
	const secret = "super-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"Authorization failed for super-secret-token"}`))
	}))
	defer server.Close()
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", server.URL)
	t.Setenv("PROMQL_OPTIMIZE_TOKEN", secret)

	var stdout bytes.Buffer
	code := run([]string{"-query", "up"}, &stdout, &bytes.Buffer{}, http.DefaultTransport)

	if code == 0 {
		t.Fatal("expected API failure")
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stdout.String(), "Authorization failed") {
		t.Fatalf("sensitive data leaked: %s", stdout.String())
	}
}

func TestCompareRangeSummarizesOldAndNewQueries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		switch r.URL.Query().Get("query") {
		case "old":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"job":"old"},"values":[[1,"1"],[2,"2"]]}]}}`))
		case "new":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
		default:
			t.Fatalf("unexpected query %q", r.URL.Query().Get("query"))
		}
	}))
	defer server.Close()
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", server.URL)

	var stdout bytes.Buffer
	code := run([]string{
		"-mode", "compare-range",
		"-old-query", "old",
		"-new-query", "new",
		"-start", "2026-05-22T00:00:00Z",
		"-end", "2026-05-22T00:30:00Z",
		"-step", "60s",
		"-expect-new-empty",
	}, &stdout, &bytes.Buffer{}, http.DefaultTransport)

	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stdout.String())
	}
	var result probeResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	oldSummary := result.Evidence["old"].(map[string]interface{})
	newSummary := result.Evidence["new"].(map[string]interface{})
	criteria := result.Evidence["success_criteria"].(map[string]interface{})
	if oldSummary["series_count"].(float64) != 1 || oldSummary["point_count"].(float64) != 2 {
		t.Fatalf("unexpected old summary: %#v", oldSummary)
	}
	if newSummary["series_count"].(float64) != 0 || newSummary["point_count"].(float64) != 0 {
		t.Fatalf("unexpected new summary: %#v", newSummary)
	}
	if criteria["met"] != true {
		t.Fatalf("expected criteria to be met: %#v", criteria)
	}
}

func TestCompareRangeRejectsConflictingExpectations(t *testing.T) {
	t.Setenv("PROMQL_OPTIMIZE_BASE_URL", "http://127.0.0.1:9090")
	var stdout bytes.Buffer

	code := run([]string{
		"-mode", "compare-range",
		"-old-query", "old",
		"-new-query", "new",
		"-start", "2026-05-22T00:00:00Z",
		"-end", "2026-05-22T00:30:00Z",
		"-expect-new-empty",
		"-expect-new-nonempty",
	}, &stdout, &bytes.Buffer{}, http.DefaultTransport)

	if code == 0 {
		t.Fatal("expected conflicting expectations failure")
	}
	if !strings.Contains(stdout.String(), "cannot both be set") {
		t.Fatalf("expected conflict message, got %s", stdout.String())
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
