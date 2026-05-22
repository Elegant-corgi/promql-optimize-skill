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
