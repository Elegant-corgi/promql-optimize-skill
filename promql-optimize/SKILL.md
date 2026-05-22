---
name: promql-optimize
description: Diagnose and optimize PromQL queries for Prometheus, VictoriaMetrics, Thanos, Mimir, and compatible APIs. Use when Codex needs to analyze slow or expensive PromQL, collect safe read-only evidence from a real metrics API through environment variables, rewrite queries, explain label cardinality or range/step problems, and draft recording rule YAML without publishing or reloading rules.
---

# PromQL Optimize

Use this skill to diagnose PromQL performance problems and produce practical rewrite and recording rule suggestions. Keep all real API access read-only.

## Workflow

1. Clarify the target query and context:
   - PromQL expression, intended dashboard or alert use, time range, step, datasource type, and observed symptom.
   - If the user wants live evidence, confirm the API environment variables are set.
2. Inspect the query statically before calling an API:
   - Identify wide ranges, high-cardinality labels, regex matchers, joins, subqueries, histogram usage, aggregation order, and repeated expensive expressions.
   - Load `references/promql-optimization-patterns.md` for detailed rewrite patterns.
3. Collect live evidence only when useful:
   - Use `scripts/promql-probe` or run `go run ./scripts/promql-probe` from this skill directory.
   - Keep probes narrow. Prefer instant query, labels, metadata, or short query_range windows before series scans.
   - Never print tokens, custom headers, or private endpoints in the final answer.
4. Generate recommendations:
   - Give an optimized PromQL expression when possible.
   - Draft recording rule YAML when the query repeats expensive work or powers dashboards/alerts.
   - Load `references/rule-generation.md` for rule naming and YAML conventions.
5. Report results in this order:
   - Diagnosis summary.
   - Evidence from API or static analysis.
   - Why the query is slow or risky.
   - Optimized PromQL.
   - Recording rule YAML draft.
   - Verification commands or checks.
   - Risks and tradeoffs.

## Live API Configuration

Read connection settings from environment variables only:

- `PROMQL_OPTIMIZE_BASE_URL`: required API base URL, for example `https://prometheus.example.com`.
- `PROMQL_OPTIMIZE_DATASOURCE`: optional, `prometheus` or `victoriametrics`.
- `PROMQL_OPTIMIZE_TOKEN`: optional Bearer token.
- `PROMQL_OPTIMIZE_HEADERS`: optional JSON object of additional headers.
- `PROMQL_OPTIMIZE_TIMEOUT`: optional HTTP timeout, default `15s`.
- `PROMQL_OPTIMIZE_MAX_RANGE`: optional maximum query_range window, default `6h`.
- `PROMQL_OPTIMIZE_MAX_LABEL_VALUES`: optional maximum label values to include in output, default `200`.
- `PROMQL_OPTIMIZE_MAX_SERIES_MATCHERS`: optional maximum number of series matchers accepted in one probe, default `5`.

Do not write these values to files. Do not include tokens, auth headers, or internal hostnames in public artifacts.

## Probe Examples

Run from the repository root or the skill directory:

```powershell
go run ./promql-optimize/scripts/promql-probe -query 'sum(rate(http_requests_total[5m]))'
go run ./promql-optimize/scripts/promql-probe -mode range -query 'sum(rate(http_requests_total[5m]))' -start 2026-05-22T00:00:00Z -end 2026-05-22T01:00:00Z -step 60s
go run ./promql-optimize/scripts/promql-probe -mode label-values -label job
go run ./promql-optimize/scripts/promql-probe -mode metadata -metric http_requests_total
```

The probe output is JSON. Treat it as evidence, not as the full diagnosis.

## Safety Rules

- Use only read-only API endpoints.
- Prefer smaller time windows and coarse steps during investigation.
- Do not use live series scans as the first action for vague or very broad queries.
- Do not publish generated recording rules, call reload endpoints, or modify remote systems.
- If a probe is blocked by safety limits, explain the limit and ask the user for a narrower query or shorter range.
