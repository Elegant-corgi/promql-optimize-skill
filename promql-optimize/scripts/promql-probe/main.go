package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	BaseURL           string
	Datasource        string
	Timeout           time.Duration
	MaxRange          time.Duration
	MaxLabelValues    int
	MaxSeriesMatchers int
	Headers           map[string]string
}

type probeResult struct {
	Status     string                 `json:"status"`
	Mode       string                 `json:"mode"`
	Datasource string                 `json:"datasource,omitempty"`
	Evidence   map[string]interface{} `json:"evidence,omitempty"`
	Warnings   []string               `json:"warnings,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

type apiEnvelope struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	Error     string          `json:"error"`
	ErrorType string          `json:"errorType"`
}

type rangeSummary struct {
	SeriesCount int                      `json:"series_count"`
	PointCount  int                      `json:"point_count"`
	Sample      []map[string]interface{} `json:"sample,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, http.DefaultTransport))
}

func run(args []string, stdout, stderr io.Writer, transport http.RoundTripper) int {
	var mode string
	var query string
	var oldQuery string
	var newQuery string
	var start string
	var end string
	var step string
	var label string
	var metric string
	var matcherList string
	var expectNewEmpty bool
	var expectNewNonEmpty bool

	flags := flag.NewFlagSet("promql-probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&mode, "mode", "query", "probe mode: query, range, compare-range, series, labels, label-values, metadata")
	flags.StringVar(&query, "query", "", "PromQL query for query or range mode")
	flags.StringVar(&oldQuery, "old-query", "", "baseline PromQL query for compare-range mode")
	flags.StringVar(&newQuery, "new-query", "", "candidate PromQL query for compare-range mode")
	flags.StringVar(&start, "start", "", "range start as RFC3339 or unix seconds")
	flags.StringVar(&end, "end", "", "range end as RFC3339 or unix seconds")
	flags.StringVar(&step, "step", "60s", "range step duration or seconds")
	flags.StringVar(&label, "label", "", "label name for label-values mode")
	flags.StringVar(&metric, "metric", "", "metric name for metadata mode")
	flags.StringVar(&matcherList, "matchers", "", "comma-separated series matchers for series mode")
	flags.BoolVar(&expectNewEmpty, "expect-new-empty", false, "compare-range success criterion: candidate query returns no samples")
	flags.BoolVar(&expectNewNonEmpty, "expect-new-nonempty", false, "compare-range success criterion: candidate query returns samples")
	if err := flags.Parse(args); err != nil {
		writeJSON(stdout, probeResult{Status: "error", Error: err.Error()})
		return 2
	}

	cfg, err := loadConfig()
	if err != nil {
		writeJSON(stdout, probeResult{Status: "error", Mode: mode, Error: err.Error()})
		return 2
	}

	client := &http.Client{Timeout: cfg.Timeout, Transport: authTransport{base: transport, headers: cfg.Headers}}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	result := probeResult{
		Status:     "success",
		Mode:       mode,
		Datasource: cfg.Datasource,
		Evidence:   map[string]interface{}{},
	}

	switch mode {
	case "query":
		if query == "" {
			return fail(stdout, result, "-query is required for query mode")
		}
		if err := validateQueryEscaping(query); err != nil {
			return fail(stdout, result, err.Error())
		}
		data, took, err := callAPI(ctx, client, cfg, "/api/v1/query", url.Values{"query": {query}})
		if err != nil {
			return fail(stdout, result, err.Error())
		}
		result.Evidence["endpoint"] = "/api/v1/query"
		result.Evidence["duration_ms"] = took.Milliseconds()
		result.Evidence["result"] = summarizeData(data, cfg.MaxLabelValues)
	case "range":
		if query == "" {
			return fail(stdout, result, "-query is required for range mode")
		}
		if err := validateQueryEscaping(query); err != nil {
			return fail(stdout, result, err.Error())
		}
		startTime, endTime, err := parseRange(start, end)
		if err != nil {
			return fail(stdout, result, err.Error())
		}
		if endTime.Sub(startTime) > cfg.MaxRange {
			return fail(stdout, result, fmt.Sprintf("range %s exceeds safety limit %s; narrow the window or set PROMQL_OPTIMIZE_MAX_RANGE", endTime.Sub(startTime), cfg.MaxRange))
		}
		stepValue, err := parseStep(step)
		if err != nil {
			return fail(stdout, result, err.Error())
		}
		data, took, err := callAPI(ctx, client, cfg, "/api/v1/query_range", url.Values{
			"query": {query},
			"start": {formatPromTime(startTime)},
			"end":   {formatPromTime(endTime)},
			"step":  {stepValue},
		})
		if err != nil {
			return fail(stdout, result, err.Error())
		}
		result.Evidence["endpoint"] = "/api/v1/query_range"
		result.Evidence["duration_ms"] = took.Milliseconds()
		result.Evidence["range_seconds"] = int64(endTime.Sub(startTime).Seconds())
		result.Evidence["result"] = summarizeData(data, cfg.MaxLabelValues)
	case "compare-range":
		if oldQuery == "" {
			return fail(stdout, result, "-old-query is required for compare-range mode")
		}
		if newQuery == "" {
			return fail(stdout, result, "-new-query is required for compare-range mode")
		}
		if expectNewEmpty && expectNewNonEmpty {
			return fail(stdout, result, "-expect-new-empty and -expect-new-nonempty cannot both be set")
		}
		if err := validateQueryEscaping(oldQuery); err != nil {
			return fail(stdout, result, err.Error())
		}
		if err := validateQueryEscaping(newQuery); err != nil {
			return fail(stdout, result, err.Error())
		}
		startTime, endTime, err := parseRange(start, end)
		if err != nil {
			return fail(stdout, result, err.Error())
		}
		if endTime.Sub(startTime) > cfg.MaxRange {
			return fail(stdout, result, fmt.Sprintf("range %s exceeds safety limit %s; narrow the window or set PROMQL_OPTIMIZE_MAX_RANGE", endTime.Sub(startTime), cfg.MaxRange))
		}
		stepValue, err := parseStep(step)
		if err != nil {
			return fail(stdout, result, err.Error())
		}
		values := func(q string) url.Values {
			return url.Values{
				"query": {q},
				"start": {formatPromTime(startTime)},
				"end":   {formatPromTime(endTime)},
				"step":  {stepValue},
			}
		}
		oldData, oldTook, err := callAPI(ctx, client, cfg, "/api/v1/query_range", values(oldQuery))
		if err != nil {
			return fail(stdout, result, "old query failed: "+err.Error())
		}
		newData, newTook, err := callAPI(ctx, client, cfg, "/api/v1/query_range", values(newQuery))
		if err != nil {
			return fail(stdout, result, "new query failed: "+err.Error())
		}
		oldSummary := summarizeRangeData(oldData, cfg.MaxLabelValues)
		newSummary := summarizeRangeData(newData, cfg.MaxLabelValues)
		result.Evidence["endpoint"] = "/api/v1/query_range"
		result.Evidence["old_duration_ms"] = oldTook.Milliseconds()
		result.Evidence["new_duration_ms"] = newTook.Milliseconds()
		result.Evidence["range_seconds"] = int64(endTime.Sub(startTime).Seconds())
		result.Evidence["old"] = oldSummary
		result.Evidence["new"] = newSummary
		criteria := map[string]interface{}{
			"old_has_samples": oldSummary.PointCount > 0,
			"new_has_samples": newSummary.PointCount > 0,
		}
		if expectNewEmpty {
			criteria["expectation"] = "new_empty"
			criteria["met"] = newSummary.PointCount == 0
		}
		if expectNewNonEmpty {
			criteria["expectation"] = "new_nonempty"
			criteria["met"] = newSummary.PointCount > 0
		}
		result.Evidence["success_criteria"] = criteria
	case "series":
		matchers := splitCSV(matcherList)
		if len(matchers) == 0 {
			return fail(stdout, result, "-matchers is required for series mode")
		}
		if len(matchers) > cfg.MaxSeriesMatchers {
			return fail(stdout, result, fmt.Sprintf("%d matchers exceeds safety limit %d", len(matchers), cfg.MaxSeriesMatchers))
		}
		values := url.Values{}
		for _, matcher := range matchers {
			values.Add("match[]", matcher)
		}
		data, took, err := callAPI(ctx, client, cfg, "/api/v1/series", values)
		if err != nil {
			return fail(stdout, result, err.Error())
		}
		result.Evidence["endpoint"] = "/api/v1/series"
		result.Evidence["duration_ms"] = took.Milliseconds()
		result.Evidence["result"] = summarizeData(data, cfg.MaxLabelValues)
	case "labels":
		data, took, err := callAPI(ctx, client, cfg, "/api/v1/labels", nil)
		if err != nil {
			return fail(stdout, result, err.Error())
		}
		result.Evidence["endpoint"] = "/api/v1/labels"
		result.Evidence["duration_ms"] = took.Milliseconds()
		result.Evidence["result"] = summarizeData(data, cfg.MaxLabelValues)
	case "label-values":
		if label == "" {
			return fail(stdout, result, "-label is required for label-values mode")
		}
		data, took, err := callAPI(ctx, client, cfg, "/api/v1/label/"+url.PathEscape(label)+"/values", nil)
		if err != nil {
			return fail(stdout, result, err.Error())
		}
		result.Evidence["endpoint"] = "/api/v1/label/<name>/values"
		result.Evidence["duration_ms"] = took.Milliseconds()
		result.Evidence["label"] = label
		result.Evidence["result"] = summarizeData(data, cfg.MaxLabelValues)
	case "metadata":
		values := url.Values{}
		if metric != "" {
			values.Set("metric", metric)
		}
		data, took, err := callAPI(ctx, client, cfg, "/api/v1/metadata", values)
		if err != nil {
			return fail(stdout, result, err.Error())
		}
		result.Evidence["endpoint"] = "/api/v1/metadata"
		result.Evidence["duration_ms"] = took.Milliseconds()
		result.Evidence["metric"] = metric
		result.Evidence["result"] = summarizeData(data, cfg.MaxLabelValues)
	default:
		return fail(stdout, result, "unsupported mode: "+mode)
	}

	writeJSON(stdout, result)
	return 0
}

func loadConfig() (config, error) {
	baseURL := strings.TrimRight(os.Getenv("PROMQL_OPTIMIZE_BASE_URL"), "/")
	if baseURL == "" {
		return config{}, errors.New("PROMQL_OPTIMIZE_BASE_URL is required")
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return config{}, errors.New("PROMQL_OPTIMIZE_BASE_URL is invalid")
	}

	timeout := durationEnv("PROMQL_OPTIMIZE_TIMEOUT", 15*time.Second)
	maxRange := durationEnv("PROMQL_OPTIMIZE_MAX_RANGE", 6*time.Hour)
	maxLabelValues := intEnv("PROMQL_OPTIMIZE_MAX_LABEL_VALUES", 200)
	maxSeriesMatchers := intEnv("PROMQL_OPTIMIZE_MAX_SERIES_MATCHERS", 5)

	headers := map[string]string{}
	if token := os.Getenv("PROMQL_OPTIMIZE_TOKEN"); token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if rawHeaders := os.Getenv("PROMQL_OPTIMIZE_HEADERS"); rawHeaders != "" {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(rawHeaders), &parsed); err != nil {
			return config{}, errors.New("PROMQL_OPTIMIZE_HEADERS must be a JSON object of string headers")
		}
		for key, value := range parsed {
			if strings.TrimSpace(key) != "" {
				headers[key] = value
			}
		}
	}

	datasource := os.Getenv("PROMQL_OPTIMIZE_DATASOURCE")
	if datasource == "" {
		datasource = "prometheus-compatible"
	}

	return config{
		BaseURL:           baseURL,
		Datasource:        datasource,
		Timeout:           timeout,
		MaxRange:          maxRange,
		MaxLabelValues:    maxLabelValues,
		MaxSeriesMatchers: maxSeriesMatchers,
		Headers:           headers,
	}, nil
}

func callAPI(ctx context.Context, client *http.Client, cfg config, path string, values url.Values) (json.RawMessage, time.Duration, error) {
	endpoint := cfg.BaseURL + path
	if values != nil && len(values) > 0 {
		endpoint += "?" + values.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, errors.New("failed to build request")
	}
	start := time.Now()
	resp, err := client.Do(req)
	took := time.Since(start)
	if err != nil {
		return nil, took, errors.New("request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, took, errors.New("failed to read response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, took, fmt.Errorf("API returned HTTP %d%s", resp.StatusCode, formatAPIError(body))
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, took, errors.New("API returned invalid JSON")
	}
	if envelope.Status != "" && envelope.Status != "success" {
		if envelope.Error != "" {
			return nil, took, errors.New("API error: " + envelope.Error)
		}
		return nil, took, errors.New("API status: " + envelope.Status)
	}
	return envelope.Data, took, nil
}

func formatAPIError(body []byte) string {
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil {
		parts := []string{}
		if envelope.ErrorType != "" {
			parts = append(parts, "errorType="+sanitizeAPIError(envelope.ErrorType))
		}
		if envelope.Error != "" {
			parts = append(parts, "error="+sanitizeAPIError(envelope.Error))
		}
		if len(parts) > 0 {
			return " (" + strings.Join(parts, "; ") + ")"
		}
	}
	return ""
}

func sanitizeAPIError(value string) string {
	replacements := []string{
		os.Getenv("PROMQL_OPTIMIZE_TOKEN"),
		os.Getenv("PROMQL_OPTIMIZE_HEADERS"),
	}
	for _, sensitive := range replacements {
		if sensitive != "" {
			value = strings.ReplaceAll(value, sensitive, "[redacted]")
		}
	}
	for _, marker := range []string{"Authorization", "Cookie", "Set-Cookie"} {
		if strings.Contains(strings.ToLower(value), strings.ToLower(marker)) {
			return "[redacted sensitive API error]"
		}
	}
	if len(value) > 1000 {
		return value[:1000] + "...[truncated]"
	}
	return value
}

func validateQueryEscaping(query string) error {
	if !strings.Contains(query, `\"`) {
		return nil
	}
	fixed := strings.ReplaceAll(query, `\"`, `"`)
	return fmt.Errorf("PromQL query contains backslash-escaped double quotes (`\\\"`). In a PowerShell single-quoted argument, keep selector quotes as plain double quotes, for example `-query 'up{job=\"snmp_exporter\"}'`; do not write `\\\"`. Suggested query: %s", fixed)
}

func summarizeData(data json.RawMessage, limit int) map[string]interface{} {
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return map[string]interface{}{"raw_bytes": len(data)}
	}
	summary := map[string]interface{}{"type": fmt.Sprintf("%T", value)}
	switch typed := value.(type) {
	case []interface{}:
		summary["count"] = len(typed)
		summary["sample"] = truncateSlice(typed, limit)
	case map[string]interface{}:
		summary["keys"] = keysOf(typed, limit)
		if result, ok := typed["result"].([]interface{}); ok {
			summary["result_count"] = len(result)
			summary["result_sample"] = truncateSlice(result, min(limit, 10))
		}
		if resultType, ok := typed["resultType"].(string); ok {
			summary["result_type"] = resultType
		}
	default:
		summary["value"] = typed
	}
	return summary
}

func summarizeRangeData(data json.RawMessage, limit int) rangeSummary {
	var payload struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return rangeSummary{}
	}
	summary := rangeSummary{SeriesCount: len(payload.Result)}
	sampleLimit := min(limit, 10)
	for _, series := range payload.Result {
		pointCount := len(series.Values)
		summary.PointCount += pointCount
		if len(summary.Sample) >= sampleLimit || pointCount == 0 {
			continue
		}
		first := series.Values[0]
		last := series.Values[pointCount-1]
		summary.Sample = append(summary.Sample, map[string]interface{}{
			"metric":      series.Metric,
			"first":       summarizeRangePoint(first),
			"last":        summarizeRangePoint(last),
			"point_count": pointCount,
		})
	}
	return summary
}

func summarizeRangePoint(point []interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	if len(point) >= 1 {
		out["timestamp"] = point[0]
	}
	if len(point) >= 2 {
		out["value"] = point[1]
	}
	return out
}

type authTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for key, value := range t.headers {
		req.Header.Set(key, value)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func fail(stdout io.Writer, result probeResult, message string) int {
	result.Status = "error"
	result.Error = message
	writeJSON(stdout, result)
	return 2
}

func writeJSON(w io.Writer, value interface{}) {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}

func parseRange(startValue, endValue string) (time.Time, time.Time, error) {
	if startValue == "" || endValue == "" {
		return time.Time{}, time.Time{}, errors.New("-start and -end are required for range mode")
	}
	start, err := parseTime(startValue)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid -start")
	}
	end, err := parseTime(endValue)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid -end")
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, errors.New("-end must be after -start")
	}
	return start, end, nil
}

func parseTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(int64(seconds), int64((seconds-float64(int64(seconds)))*1e9)).UTC(), nil
}

func parseStep(value string) (string, error) {
	if value == "" {
		return "", errors.New("-step is required")
	}
	if _, err := time.ParseDuration(value); err == nil {
		return value, nil
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return value, nil
	}
	return "", errors.New("invalid -step")
}

func formatPromTime(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixNano())/1e9, 'f', -1, 64)
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func intEnv(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func truncateSlice(values []interface{}, limit int) []interface{} {
	if limit < 0 {
		limit = 0
	}
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func keysOf(values map[string]interface{}, limit int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
		if len(keys) >= limit {
			break
		}
	}
	return keys
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
