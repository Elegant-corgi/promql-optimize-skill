---
name: promql-optimize
description: Diagnose and optimize PromQL queries for Prometheus, VictoriaMetrics, Thanos, Mimir, and compatible APIs. Use when Codex needs to analyze slow or expensive PromQL, optionally collect safe read-only evidence from a real metrics API only after explicit user confirmation, use environment variables or an explicitly selected local wrapper profile, rewrite queries, explain label cardinality or range/step problems, and draft recording rule YAML without publishing or reloading rules.
---

# PromQL Optimize

Use this skill to diagnose PromQL performance problems and produce practical rewrite and recording rule suggestions. Keep all real API access read-only.

## Workflow

1. Clarify the target query and context:
   - PromQL expression, intended dashboard or alert use, time range, step, datasource type, and observed symptom.
   - Apply the confirmation gate before reading local profiles, checking environment variables, running helper scripts, or giving optimization output.
   - Treat datasource names, profile names, cluster names, or lines like `数据源：<name>` as context only. They are not consent to use a real datasource and are not proof that a real API environment is available.
   - Treat only explicit phrases such as "use real datasource", "live API evidence", "allow probe", "调用真实 API", "允许探测", or "使用真实数据源" as intent to use a real datasource.
   - If the user asks to optimize or analyze PromQL but no usable real environment is recognized, stop before optimization output and present exactly these choices:
     ```text
     未识别到可用真实环境。请选择本次分析方式：
     1. 使用本地静态环境配置：我会列出可用 profile，请输入序号或名称确认。
     2. 使用本次会话临时环境变量：我会提示需要设置哪些 PROMQL_OPTIMIZE_* 变量；如需 token，请按示例在本机环境中配置。
     3. 不使用真实环境：直接进行静态 PromQL 优化。
     ```
   - A usable real environment means either `PROMQL_OPTIMIZE_BASE_URL` is already set in the current process, or the user has explicitly selected a local wrapper profile for this session.
   - For choice 1, you may inspect local profile metadata with `Get-PromQLProfileList`, then show only ordinal number, profile name, aliases, datasource, token/header environment variable names, and whether those variables are configured. Never print `baseUrl`, token values, header values, or private endpoints.
   - For choice 2, ask the user to configure at least `PROMQL_OPTIMIZE_BASE_URL` for the current session. If a token or headers are needed, provide local PowerShell configuration commands and let the user set them locally; do not ask them to paste secrets into chat.
   - For choice 3, continue with static optimization only after the user confirms static-only analysis.
   - If the user explicitly asks for real datasource usage, first confirm the environment variables are set or the user has selected a local wrapper profile. Only then inspect environment/profile state or run probes.
2. Inspect the query statically before calling an API:
   - Identify wide ranges, high-cardinality labels, regex matchers, joins, subqueries, histogram usage, aggregation order, and repeated expensive expressions.
   - Load `references/promql-optimization-patterns.md` for detailed rewrite patterns.
3. Collect live evidence only when useful:
   - Do not inspect or call local configuration, defaults, profiles, or `promql-probe` merely because a datasource/profile name appears in the prompt; require explicit user intent to use a real datasource.
   - If the user has selected a local wrapper profile, use `scripts/promql-profile.ps1` from the installed skill directory to export `PROMQL_OPTIMIZE_*` environment variables before probing. Do not print the resolved endpoint, token, headers, or private profile contents.
   - Use `scripts/promql-probe` directly only when `PROMQL_OPTIMIZE_BASE_URL` is already set in the current process.
   - Otherwise, run `go run ./scripts/promql-probe` from this skill directory after the environment has been prepared.
   - Keep probes narrow. Prefer instant query, labels, metadata, or short query_range windows before series scans.
   - PowerShell quoting rule: when passing PromQL with label selectors to `Invoke-PromQLProbe -query`, prefer single quotes around the whole query and keep selector double quotes unescaped. Correct: `Invoke-PromQLProbe -query 'up{job="snmp_exporter"}'`. Wrong: `Invoke-PromQLProbe -query 'up{job=\"snmp_exporter\"}'`.
   - If a probe returns `API returned HTTP 400` and the query text contains `\"`, treat it as a local quoting/escaping mistake first. Retry with plain selector quotes before diagnosing missing metrics, datasource incompatibility, or backend parsing behavior.
   - Never print tokens, custom headers, or private endpoints in the final answer.
4. Generate recommendations:
   - Give an optimized PromQL expression when possible.
   - Draft recording rule YAML when the query repeats expensive work or powers dashboards/alerts.
   - Load `references/rule-generation.md` for rule naming and YAML conventions.
   - When splitting one alert into peer alerts, make the split rules mutually exclusive. If a label value is handled by a dedicated peer alert, explicitly exclude that value from any generic or catch-all peer alert. Do not output overlapping peer alert rules unless the user explicitly asks for duplicate coverage or the rules are not peers.
   - For alert split recommendations, load `references/promql-optimization-patterns.md` and apply the alert split exclusivity pattern before finalizing PromQL.
5. Report results in this order:
   - Diagnosis summary.
   - Evidence from API or static analysis.
   - Why the query is slow or risky.
   - Optimized PromQL.
   - Recording rule YAML draft.
   - Alert split overlap check, if peer alert rules are recommended.
   - Verification commands or checks.
   - Risks and tradeoffs.

## Live API Configuration

Read connection settings from environment variables, optionally populated by the skill-local profile wrapper after explicit real datasource confirmation.

### Environment Variables

- `PROMQL_OPTIMIZE_BASE_URL`: required API base URL, for example `https://prometheus.example.com`.
- `PROMQL_OPTIMIZE_DATASOURCE`: optional, `prometheus` or `victoriametrics`.
- `PROMQL_OPTIMIZE_TOKEN`: optional Bearer token.
- `PROMQL_OPTIMIZE_HEADERS`: optional JSON object of additional headers.
- `PROMQL_OPTIMIZE_TIMEOUT`: optional HTTP timeout, default `15s`.
- `PROMQL_OPTIMIZE_MAX_RANGE`: optional maximum query_range window, default `6h`.
- `PROMQL_OPTIMIZE_MAX_LABEL_VALUES`: optional maximum label values to include in output, default `200`.
- `PROMQL_OPTIMIZE_MAX_SERIES_MATCHERS`: optional maximum number of series matchers accepted in one probe, default `5`.

Do not write these values to files. Do not include tokens, auth headers, or internal hostnames in public artifacts.

### Local Wrapper Profiles

The installed skill provides `scripts/promql-profile.ps1` and expects user-local configuration at `config/promql-profiles.json` inside the same skill directory. This file stores endpoint metadata and environment variable names such as `tokenEnv`; it must not store raw tokens. Create it from `config/promql-profiles.example.json` after installation.

Use `Get-PromQLProfileList` to safely list local candidates when the user chooses local static environment configuration. It returns non-sensitive metadata only.

Use the wrapper for probing only after the user explicitly asks for real API evidence and either names an existing profile or confirms the current profile should be used:

```powershell
. .\scripts\promql-profile.ps1
Get-PromQLProfileList
Use-PromQLProfile <name>
Invoke-PromQLProbe -query '<promql>'
# Correct PowerShell selector quoting:
Invoke-PromQLProbe -query 'up{job="snmp_exporter"}'
# Do not backslash-escape selector quotes in a single-quoted argument:
# Invoke-PromQLProbe -query 'up{job=\"snmp_exporter\"}'
```

If a profile is already selected, `Invoke-PromQLProbe` reads that profile, resolves token/header environment variables, sets `PROMQL_OPTIMIZE_*` for the child probe process, and runs the read-only probe. Treat profile names in the prompt as context until this explicit confirmation exists.

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
