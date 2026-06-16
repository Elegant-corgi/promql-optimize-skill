# PromQL Optimization Patterns

Use this reference after the skill triggers and the query shape is known.

## Diagnosis Checklist

- Check selector breadth: missing label constraints, broad regex, or long retention ranges.
- Check cardinality: grouping by pod, path, user, instance, container, le, or other high-cardinality labels.
- Check range and step: long range vectors, nested subqueries, small step on wide windows.
- Check joins: `group_left`, `group_right`, many-to-many risk, or metadata joins on unstable labels.
- Check aggregation order: aggregate after `rate` for counters; avoid preserving labels that are not used downstream.
- Check repeated work: dashboard panels or alerts repeatedly computing the same expensive expression.

## High-Cardinality Aggregation

Symptom:

```promql
sum by (pod, path, status) (rate(http_requests_total[30d]))
```

Problem:

- Long range vector plus grouping by high-cardinality labels multiplies samples and output series.
- `path` and `pod` often change frequently and are poor long-term dashboard dimensions.

Prefer:

```promql
sum by (service, status) (rate(http_requests_total[5m]))
```

Recording rule candidate:

```yaml
groups:
  - name: service-http.rules
    interval: 30s
    rules:
      - record: service:http_requests:rate5m
        expr: sum by (service, status) (rate(http_requests_total[5m]))
```

## Regex Matchers

Symptom:

```promql
sum(rate(container_cpu_usage_seconds_total{pod=~".*api.*"}[1h]))
```

Problem:

- Broad regex matchers can scan many label values.
- Leading `.*` prevents efficient narrowing.

Prefer exact or anchored matchers:

```promql
sum by (namespace, workload) (
  rate(container_cpu_usage_seconds_total{namespace="prod", workload="api"}[5m])
)
```

If regex is unavoidable:

```promql
sum(rate(container_cpu_usage_seconds_total{pod=~"api-[a-z0-9-]+", namespace="prod"}[5m]))
```

## Alert Split Exclusivity

Symptom:

```promql
# Generic hardware alert
ipmi_sensor_state{type!~"Entity Presence|System Event"} > 0

# Dedicated peer alert
ipmi_sensor_state{type="Event Logging Disabled"} > 0
```

Problem:

- The dedicated peer alert still matches the generic alert because `Event Logging Disabled` is not excluded from the generic selector.
- Peer alert splits should not create duplicate notifications for the same series unless the user explicitly asks for overlapping coverage.

Prefer:

```promql
# Generic hardware alert
ipmi_sensor_state{type!~"Entity Presence|System Event|Event Logging Disabled"} > 0

# Dedicated peer alert
ipmi_sensor_state{type="Event Logging Disabled"} > 0
```

Check:

- List the label values claimed by dedicated peer alerts.
- Ensure every claimed value is excluded from generic or catch-all peer alerts.
- If live evidence is available, compare candidate peer alerts with `and` or selector reasoning to confirm no shared output series.

## Alert Semantics Patterns

Use these patterns when the user's goal is alert behavior, not only query cost. Always identify what "recover" means before choosing a template.

### Trigger Once, Hold for N, Then Recover

Symptom:

```promql
error_ratio > 2
```

Goal:

- A short spike should become a continuous alert for 3 days.
- A target that has been continuously bad for the full 3 days should disappear.
- Output is boolean `1`; configure the alert threshold as `> 0`.

Prefer:

```promql
(
  max_over_time(((error_ratio) > bool 2)[3d:5m]) > 0
)
unless
(
  min_over_time(((error_ratio) > bool 2)[3d:5m]) > 0
)
```

Notes:

- `max_over_time` turns any trigger inside the window into a held alert.
- `min_over_time` identifies series that were continuously triggering for the entire window; `unless` removes them.
- Choose the subquery resolution, such as `5m`, to match the alert evaluation cadence and datasource cost.

### Recover After N Without Successful Responses

Use this when the target is still being probed, but should be treated as recovered after no successful response has been seen for N.

```promql
(error_ratio > 2)
and
(increase(successful_response_count[3d]) > 0)
```

Notes:

- This keeps the alert only while there was at least one success in the lookback window.
- It is different from "hold each trigger for 3 days"; it may produce sparse graph points if the base condition is sparse.

### Recover After N Without Requests

Use this only when request generation itself is the desired liveness signal.

```promql
(error_ratio > 2)
and
(increase(requests_total[3d]) > 0)
```

Pitfall:

- `requests_total` means the prober is still sending requests, not that the target is healthy. In smokeping-style probes it can keep increasing while the target has 100% packet loss.

### Recover After N Without Scraped Samples

Use this when disappearance of scraped samples should suppress or recover the alert.

```promql
(error_ratio > 2)
and
present_over_time(source_metric[3d])
```

For pure absence alerts, use `absent_over_time(source_metric[3d])`, but remember it returns synthetic label sets and may not preserve the original target labels.

### Verification

For any query involving "recover", "continuous", "hold", or "after N", verify with `query_range` across the full target window. With the bundled probe, compare old and new expressions:

```powershell
Invoke-PromQLProbe -Profile <profile> -mode compare-range `
  -old-query '<old promql>' `
  -new-query '<new promql>' `
  -start <start> -end <end> -step 10m `
  -expect-new-empty
```

## Wide Range and Query Range

Symptom:

- Dashboard uses 30d range with 15s step.
- Query contains `[1h:]` or nested subqueries across long windows.

Problem:

- Sample count grows with `range / step`.
- Subqueries can multiply inner and outer sample counts.

Prefer:

- Increase step for long dashboards.
- Use shorter rate windows such as `[5m]` or `[10m]` unless the signal needs smoothing.
- Precompute with recording rules for common dashboard windows.

## Joins and Metadata Enrichment

Symptom:

```promql
rate(a_total[5m]) * on(instance) group_left(version) b_info
```

Problem:

- `group_left` can expand series if the right side is not unique for the join labels.
- Metadata metrics often contain extra labels that increase result cardinality.

Check:

```promql
count by (instance) (b_info) > 1
```

Prefer:

- Join on stable, minimal labels.
- Aggregate before the join when possible.
- Precompute enriched metrics only if the enriched labels are genuinely needed.

## Histogram Quantile

Symptom:

```promql
histogram_quantile(0.99, sum by (le, pod, path) (rate(http_request_duration_seconds_bucket[5m])))
```

Problem:

- Keeping `pod` and `path` before `histogram_quantile` creates many bucket series.
- High quantiles over sparse buckets are noisy and expensive.

Prefer:

```promql
histogram_quantile(
  0.99,
  sum by (le, service) (rate(http_request_duration_seconds_bucket[5m]))
)
```

Recording rule candidate:

```yaml
groups:
  - name: service-latency.rules
    interval: 30s
    rules:
      - record: service:http_request_duration_seconds_bucket:rate5m
        expr: sum by (service, le) (rate(http_request_duration_seconds_bucket[5m]))
```

## Counter Rate Order

Use:

```promql
sum by (service) (rate(requests_total[5m]))
```

Avoid:

```promql
rate(sum by (service) (requests_total)[5m])
```

Reason:

- `rate` needs raw counter resets per series.
- Aggregating before `rate` can hide resets and produce wrong values.

## VictoriaMetrics Notes

- VictoriaMetrics supports many Prometheus-compatible endpoints, but query planner behavior can differ.
- Prefer checking actual query duration and output series count rather than assuming Prometheus behavior exactly.
- For very large deployments, use narrower label filters before `/api/v1/series`.
