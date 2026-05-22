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
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  string          `json:"error"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, http.DefaultTransport))
}

func run(args []string, stdout, stderr io.Writer, transport http.RoundTripper) int {
	var mode string
	var query string
	var start string
	var end string
	var step string
	var label string
	var metric string
	var matcherList string

	flags := flag.NewFlagSet("promql-probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&mode, "mode", "query", "probe mode: query, range, series, labels, label-values, metadata")
	flags.StringVar(&query, "query", "", "PromQL query for query or range mode")
	flags.StringVar(&start, "start", "", "range start as RFC3339 or unix seconds")
	flags.StringVar(&end, "end", "", "range end as RFC3339 or unix seconds")
	flags.StringVar(&step, "step", "60s", "range step duration or seconds")
	flags.StringVar(&label, "label", "", "label name for label-values mode")
	flags.StringVar(&metric, "metric", "", "metric name for metadata mode")
	flags.StringVar(&matcherList, "matchers", "", "comma-separated series matchers for series mode")
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
		return nil, took, fmt.Errorf("API returned HTTP %d", resp.StatusCode)
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
