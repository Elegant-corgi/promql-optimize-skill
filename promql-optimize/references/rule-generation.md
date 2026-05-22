# Recording Rule Generation

Generate YAML drafts only. Do not publish rules, call reload endpoints, or edit production rule files unless the user explicitly asks in a separate implementation task.

## When to Recommend Rules

Recommend a recording rule when:

- The same expensive expression appears in dashboards or alerts.
- The query repeatedly performs aggregation over high-cardinality raw metrics.
- Histogram bucket aggregation is reused by multiple quantile queries.
- A join enriches metrics with stable metadata and is used frequently.

Do not recommend a rule when:

- The query is one-off investigation.
- The expression depends on ad hoc labels or temporary filters.
- The rule would preserve high-cardinality labels without clear value.

## Naming

Use the pattern:

```text
<scope>:<metric_or_signal>:<operation><window>
```

Examples:

```text
service:http_requests:rate5m
service:http_request_duration_seconds_bucket:rate5m
namespace:container_cpu_usage_seconds:rate5m
```

Keep names stable, lowercase, and descriptive. Avoid embedding private tenant or environment names.

## YAML Template

```yaml
groups:
  - name: <domain>.rules
    interval: 30s
    rules:
      - record: <scope>:<signal>:<operation><window>
        expr: |
          <promql expression>
        labels:
          source: promql-optimize
```

Only include static labels when they are safe and useful. Do not include secrets, internal URLs, or user-specific values.

## Validation Suggestions

- Parse the YAML with the target rule toolchain before publishing.
- Compare old and new query results over a short window.
- Check output series count before and after the rule.
- Confirm alert semantics are unchanged when rules feed alerts.
