# promql-optimize-skill

`promql-optimize-skill` 是一个面向 Codex 的 PromQL 调优 skill。它可以帮助分析 Prometheus、VictoriaMetrics、Thanos、Mimir 等兼容数据源中的慢查询或高成本查询，并给出更合适的 PromQL 写法和 recording rule 草案。

适用场景：

- Grafana 面板查询很慢，需要定位 PromQL 成本来源。
- 告警表达式过于复杂，需要拆分为 recording rule。
- 查询中存在高基数 label、宽时间范围、正则匹配、join 或 histogram quantile，需要评估优化空间。
- 希望 Codex 结合真实 Prometheus/VictoriaMetrics API 证据，而不只做静态分析。

## 特性

- 分析常见 PromQL 性能问题。
- 支持 Prometheus 和 VictoriaMetrics 兼容 API。
- 通过只读 API 采集诊断证据。
- 使用环境变量配置连接信息，不需要配置文件。
- 支持项目级 PromQL profile wrapper，在多个后端地址之间快速切换。
- 默认限制查询范围，避免诊断过程本身给监控后端造成压力。
- 生成优化后的 PromQL 建议。
- 生成 recording rule YAML 草案。
- 不发布规则、不 reload、不修改远端系统。
- 不在输出中打印 token、认证 header 或 cookie。

## 项目结构

```text
.
|-- promql-optimize/
|   |-- SKILL.md
|   |-- agents/
|   |   `-- openai.yaml
|   |-- references/
|   |   |-- promql-optimization-patterns.md
|   |   |-- rule-generation.md
|   |   `-- safety.md
|   `-- scripts/
|       `-- promql-probe/
|           |-- go.mod
|           |-- main.go
|           `-- main_test.go
|-- scripts/
|   |-- promql-profile.ps1
|   `-- sync-skill.ps1
|-- config/
|   `-- promql-profiles.example.json
|-- go.work
`-- README.md
```

## 安装

在仓库根目录运行：

```powershell
.\scripts\sync-skill.ps1
```

脚本会把 `promql-optimize/` 安装到当前用户的 Codex skills 目录：

```text
~/.codex/skills/promql-optimize
```

安装后，可以在 Codex 中通过 `$promql-optimize` 调用这个 skill。

## 配置真实 API

如果只做静态 PromQL 分析，不需要配置任何环境变量。

如果希望连接真实 Prometheus 或 VictoriaMetrics API，至少设置：

```powershell
$env:PROMQL_OPTIMIZE_BASE_URL = "https://prometheus.example.com"
```

可选配置：

```powershell
$env:PROMQL_OPTIMIZE_DATASOURCE = "prometheus"
$env:PROMQL_OPTIMIZE_TOKEN = "<bearer-token>"
$env:PROMQL_OPTIMIZE_HEADERS = '{"X-Scope":"prod"}'
$env:PROMQL_OPTIMIZE_TIMEOUT = "15s"
$env:PROMQL_OPTIMIZE_MAX_RANGE = "6h"
$env:PROMQL_OPTIMIZE_MAX_LABEL_VALUES = "200"
$env:PROMQL_OPTIMIZE_MAX_SERIES_MATCHERS = "5"
```

认证信息只从环境变量读取。工具不会把 token 或自定义认证 header 写入文件，也不会在正常输出中打印它们。

## 多 PromQL 地址切换

如果平时需要连接多个 Prometheus 或 VictoriaMetrics 地址，可以使用项目级 profile wrapper，避免反复手动切换 `PROMQL_OPTIMIZE_*`。

先从示例创建本地配置：

```powershell
Copy-Item .\config\promql-profiles.example.json .\config\promql-profiles.json
```

编辑 `config\promql-profiles.json`，只写地址、数据源类型和 token 环境变量名，不写 token 明文：

```json
{
  "zhipu": {
    "aliases": ["智谱", "Zhipu"],
    "baseUrl": "https://prometheus.example.com",
    "datasource": "prometheus",
    "tokenEnv": "PROMQL_ZHIPU_TOKEN"
  }
}
```

一次性配置 token：

```powershell
[Environment]::SetEnvironmentVariable("PROMQL_ZHIPU_TOKEN", "你的token", "User")
```

日常使用：

```powershell
. .\scripts\promql-profile.ps1
Use-PromQLProfile zhipu
Get-PromQLProfile
Invoke-PromQLProbe -query "up"
```

`config\promql-profiles.json` 和 `config\promql-current-profile` 默认被 `.gitignore` 忽略，避免误提交内部地址和本地状态。`Get-PromQLProfile` 只显示 token 变量名和是否已配置，不会输出 token。

## 在 Codex 中使用

静态分析示例：

```text
Use $promql-optimize to analyze this PromQL:
sum by (pod, path, status) (rate(http_requests_total[30d]))

目标是优化 Grafana 面板查询，并给出 recording rule 草案。
```

结合真实 API 证据：

```text
Use $promql-optimize to analyze this PromQL with live API evidence.
Query: histogram_quantile(0.99, sum by (le, pod, path) (rate(http_request_duration_seconds_bucket[5m])))
Time range: last 1h
Step: 60s
```

Codex 会根据 skill 流程先做静态分析，再在需要时调用只读探测工具，最后输出诊断证据、优化建议、recording rule 草案和验证方式。

## promql-probe CLI

`promql-probe` 是 skill 内置的 Go 探测工具。它只调用只读 API，并输出 JSON 结果，方便 Codex 或人工进一步分析。

从仓库根目录运行 instant query：

```powershell
go run .\promql-optimize\scripts\promql-probe -query "sum(rate(http_requests_total[5m]))"
```

运行 range query：

```powershell
go run .\promql-optimize\scripts\promql-probe `
  -mode range `
  -query "sum(rate(http_requests_total[5m]))" `
  -start 2026-05-22T00:00:00Z `
  -end 2026-05-22T01:00:00Z `
  -step 60s
```

查询 label values：

```powershell
go run .\promql-optimize\scripts\promql-probe -mode label-values -label job
```

查询 metric metadata：

```powershell
go run .\promql-optimize\scripts\promql-probe -mode metadata -metric http_requests_total
```

没有配置 API 地址时，工具会返回结构化错误：

```json
{
  "status": "error",
  "mode": "query",
  "error": "PROMQL_OPTIMIZE_BASE_URL is required"
}
```

## 安全默认值

默认限制：

- HTTP timeout: `15s`
- 最大 range query 窗口: `6h`
- 最大输出 label values: `200`
- 单次 series 探测最大 matcher 数: `5`

这些限制可以通过环境变量调整。对于大规模集群，建议先缩小 selector 和时间范围，再逐步扩大诊断范围。

## 开发

运行测试：

```powershell
$env:GOCACHE = Join-Path (Get-Location) ".gocache"
go test .\promql-optimize\scripts\promql-probe
```

同步本地 skill：

```powershell
.\scripts\sync-skill.ps1
```
