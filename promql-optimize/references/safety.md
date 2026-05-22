# Safety Boundaries

The skill and probe must stay read-only by default.

## Never Do Implicitly

- Do not call reload endpoints.
- Do not write to remote rule APIs.
- Do not commit tokens, endpoints, metric names from private systems, or label values from real tenants.
- Do not run unbounded `/api/v1/series` probes.
- Do not expand safety limits silently.

## Redaction Rules

- Treat `Authorization`, `X-Auth-*`, `Cookie`, and `Set-Cookie` as sensitive.
- Do not print `PROMQL_OPTIMIZE_TOKEN`.
- Do not echo `PROMQL_OPTIMIZE_HEADERS`.
- In public examples, use generic metrics such as `http_requests_total`.

## Conservative Defaults

- HTTP timeout: `15s`.
- Maximum `query_range` window: `6h`.
- Maximum label values included in output: `200`.
- Maximum series matchers per probe: `5`.

If these limits block diagnosis, ask the user to narrow the query or explicitly set a larger environment variable for the next run.
