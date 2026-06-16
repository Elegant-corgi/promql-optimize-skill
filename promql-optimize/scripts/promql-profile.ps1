Set-StrictMode -Version Latest

$script:PromQLSkillRoot = Split-Path -Parent $PSScriptRoot
$script:PromQLProfileConfigPath = Join-Path $script:PromQLSkillRoot "config\promql-profiles.json"
$script:PromQLProfileExamplePath = Join-Path $script:PromQLSkillRoot "config\promql-profiles.example.json"
$script:PromQLCurrentProfilePath = Join-Path $script:PromQLSkillRoot "config\promql-current-profile"
$script:PromQLProbeDir = Join-Path $script:PromQLSkillRoot "scripts\promql-probe"

function Read-PromQLProfileConfig {
    param([string]$ConfigPath = $script:PromQLProfileConfigPath)

    if (-not (Test-Path -LiteralPath $ConfigPath)) {
        throw "PromQL profile 配置不存在：$ConfigPath。可从示例复制：$script:PromQLProfileExamplePath"
    }

    try {
        $raw = Get-Content -LiteralPath $ConfigPath -Raw -Encoding UTF8
        if ([string]::IsNullOrWhiteSpace($raw)) {
            throw "配置文件为空"
        }
        return $raw | ConvertFrom-Json
    }
    catch {
        throw "PromQL profile 配置无法解析：$($_.Exception.Message)"
    }
}

function Get-PromQLProfileNames {
    param([object]$Config)
    return @($Config.PSObject.Properties | ForEach-Object { $_.Name })
}

function Resolve-PromQLProfile {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name,

        [Parameter(Mandatory = $true)]
        [object]$Config
    )

    $names = Get-PromQLProfileNames -Config $Config
    foreach ($profileName in $names) {
        if ($profileName -eq $Name) {
            return $profileName
        }

        $profile = $Config.$profileName
        if ($null -ne $profile.PSObject.Properties["aliases"]) {
            foreach ($alias in @($profile.aliases)) {
                if ([string]$alias -eq $Name) {
                    return $profileName
                }
            }
        }
    }

    throw "未知 PromQL profile：$Name。可用 profile：$($names -join ', ')"
}

function Test-PromQLProfile {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name,

        [Parameter(Mandatory = $true)]
        [object]$Profile
    )

    if ($null -eq $Profile.PSObject.Properties["baseUrl"] -or [string]::IsNullOrWhiteSpace([string]$Profile.baseUrl)) {
        throw "PromQL profile '$Name' 缺少 baseUrl。"
    }

    $uri = $null
    if (-not [System.Uri]::TryCreate([string]$Profile.baseUrl, [System.UriKind]::Absolute, [ref]$uri)) {
        throw "PromQL profile '$Name' 的 baseUrl 不是有效 URL：$($Profile.baseUrl)"
    }
    if ($uri.Scheme -notin @("http", "https")) {
        throw "PromQL profile '$Name' 的 baseUrl 只支持 http 或 https：$($Profile.baseUrl)"
    }

    if ($null -ne $Profile.PSObject.Properties["datasource"]) {
        $datasource = [string]$Profile.datasource
        if ($datasource -and $datasource -notin @("prometheus", "victoriametrics")) {
            throw "PromQL profile '$Name' 的 datasource 只支持 prometheus 或 victoriametrics：$datasource"
        }
    }
}

function Get-PromQLEnvValue {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name
    )

    $value = [Environment]::GetEnvironmentVariable($Name, "Process")
    if ([string]::IsNullOrEmpty($value)) {
        $value = [Environment]::GetEnvironmentVariable($Name, "User")
    }
    if ([string]::IsNullOrEmpty($value)) {
        $value = [Environment]::GetEnvironmentVariable($Name, "Machine")
    }
    return $value
}

function Get-PromQLProfileList {
    $config = Read-PromQLProfileConfig
    $profiles = @()

    foreach ($profileName in (Get-PromQLProfileNames -Config $config)) {
        $profile = $config.$profileName
        Test-PromQLProfile -Name $profileName -Profile $profile

        $aliases = if ($null -ne $profile.PSObject.Properties["aliases"]) { @($profile.aliases) } else { @() }
        $tokenEnv = if ($null -ne $profile.PSObject.Properties["tokenEnv"]) { [string]$profile.tokenEnv } else { $null }
        $headersEnv = if ($null -ne $profile.PSObject.Properties["headersEnv"]) { [string]$profile.headersEnv } else { $null }
        $datasource = if ($null -ne $profile.PSObject.Properties["datasource"] -and -not [string]::IsNullOrWhiteSpace([string]$profile.datasource)) { [string]$profile.datasource } else { "prometheus" }

        $profiles += [pscustomobject]@{
            name = $profileName
            aliases = $aliases
            datasource = $datasource
            tokenEnv = $tokenEnv
            tokenConfigured = if ($tokenEnv) { -not [string]::IsNullOrEmpty((Get-PromQLEnvValue -Name $tokenEnv)) } else { $false }
            headersEnv = $headersEnv
            headersConfigured = if ($headersEnv) { -not [string]::IsNullOrEmpty((Get-PromQLEnvValue -Name $headersEnv)) } else { $false }
        }
    }

    return $profiles
}

function Use-PromQLProfile {
    param(
        [Parameter(Mandatory = $true, Position = 0)]
        [string]$Name
    )

    $config = Read-PromQLProfileConfig
    $resolvedName = Resolve-PromQLProfile -Name $Name -Config $config
    $profile = $config.$resolvedName
    Test-PromQLProfile -Name $resolvedName -Profile $profile

    $directory = Split-Path -Parent $script:PromQLCurrentProfilePath
    if (-not (Test-Path -LiteralPath $directory)) {
        New-Item -ItemType Directory -Path $directory | Out-Null
    }

    Set-Content -LiteralPath $script:PromQLCurrentProfilePath -Value $resolvedName -Encoding UTF8 -NoNewline
    Write-Host "已切换 PromQL profile：$resolvedName"
}

function Get-PromQLProfile {
    param([string]$Name)

    $config = Read-PromQLProfileConfig
    if ([string]::IsNullOrWhiteSpace($Name)) {
        if (-not (Test-Path -LiteralPath $script:PromQLCurrentProfilePath)) {
            throw "当前 PromQL profile 未设置，请先执行 Use-PromQLProfile <name>。"
        }
        $Name = (Get-Content -LiteralPath $script:PromQLCurrentProfilePath -Raw -Encoding UTF8).Trim()
    }

    $resolvedName = Resolve-PromQLProfile -Name $Name -Config $config
    $profile = $config.$resolvedName
    Test-PromQLProfile -Name $resolvedName -Profile $profile

    $tokenEnv = if ($null -ne $profile.PSObject.Properties["tokenEnv"]) { [string]$profile.tokenEnv } else { $null }
    $headersEnv = if ($null -ne $profile.PSObject.Properties["headersEnv"]) { [string]$profile.headersEnv } else { $null }
    $datasource = if ($null -ne $profile.PSObject.Properties["datasource"]) { [string]$profile.datasource } else { "prometheus" }

    [pscustomobject]@{
        name = $resolvedName
        baseUrl = [string]$profile.baseUrl
        datasource = $datasource
        tokenEnv = $tokenEnv
        tokenConfigured = if ($tokenEnv) { -not [string]::IsNullOrEmpty((Get-PromQLEnvValue -Name $tokenEnv)) } else { $false }
        headersEnv = $headersEnv
        headersConfigured = if ($headersEnv) { -not [string]::IsNullOrEmpty((Get-PromQLEnvValue -Name $headersEnv)) } else { $false }
    }
}

function Set-PromQLProbeEnvironment {
    param(
        [Parameter(Mandatory = $true)]
        [object]$ProfileInfo,

        [Parameter(Mandatory = $true)]
        [object]$RawProfile
    )

    $env:PROMQL_OPTIMIZE_BASE_URL = $ProfileInfo.baseUrl
    $env:PROMQL_OPTIMIZE_DATASOURCE = $ProfileInfo.datasource

    if ($ProfileInfo.tokenEnv) {
        $token = Get-PromQLEnvValue -Name $ProfileInfo.tokenEnv
        if ([string]::IsNullOrEmpty($token)) {
            throw "PromQL profile '$($ProfileInfo.name)' 需要 token 环境变量 '$($ProfileInfo.tokenEnv)'，但当前未配置。"
        }
        $env:PROMQL_OPTIMIZE_TOKEN = $token
    }
    else {
        Remove-Item Env:\PROMQL_OPTIMIZE_TOKEN -ErrorAction SilentlyContinue
    }

    if ($ProfileInfo.headersEnv) {
        $headers = Get-PromQLEnvValue -Name $ProfileInfo.headersEnv
        if ([string]::IsNullOrEmpty($headers)) {
            throw "PromQL profile '$($ProfileInfo.name)' 需要 headers 环境变量 '$($ProfileInfo.headersEnv)'，但当前未配置。"
        }
        $env:PROMQL_OPTIMIZE_HEADERS = $headers
    }
    else {
        Remove-Item Env:\PROMQL_OPTIMIZE_HEADERS -ErrorAction SilentlyContinue
    }

    foreach ($pair in @(
        @("timeout", "PROMQL_OPTIMIZE_TIMEOUT"),
        @("maxRange", "PROMQL_OPTIMIZE_MAX_RANGE"),
        @("maxLabelValues", "PROMQL_OPTIMIZE_MAX_LABEL_VALUES"),
        @("maxSeriesMatchers", "PROMQL_OPTIMIZE_MAX_SERIES_MATCHERS")
    )) {
        $propertyName = $pair[0]
        $envName = $pair[1]
        if ($null -ne $RawProfile.PSObject.Properties[$propertyName] -and -not [string]::IsNullOrWhiteSpace([string]$RawProfile.$propertyName)) {
            Set-Item -Path "Env:\$envName" -Value ([string]$RawProfile.$propertyName)
        }
    }
}

function Get-PromQLProbeEscapingError {
    param(
        [string[]]$Arguments
    )

    for ($i = 0; $i -lt $Arguments.Count; $i++) {
        $argument = $Arguments[$i]
        $query = $null
        if ($argument -in @("-query", "-old-query", "-new-query") -and ($i + 1) -lt $Arguments.Count) {
            $query = $Arguments[$i + 1]
        }
        elseif ($argument.StartsWith("-query=") -or $argument.StartsWith("-old-query=") -or $argument.StartsWith("-new-query=")) {
            $query = $argument.Substring($argument.IndexOf("=") + 1)
        }

        if ($null -ne $query -and $query.Contains('\"')) {
            $fixed = $query.Replace('\"', '"')
            return 'PromQL query contains backslash-escaped double quotes (`\"`). In a PowerShell single-quoted argument, keep selector quotes as plain double quotes, for example `-query ''up{job="snmp_exporter"}''`; do not write `\"`. Suggested query: ' + $fixed
        }
    }

    return $null
}

function Get-PromQLProbeMode {
    param(
        [string[]]$Arguments
    )

    for ($i = 0; $i -lt $Arguments.Count; $i++) {
        $argument = $Arguments[$i]
        if ($argument -eq "-mode" -and ($i + 1) -lt $Arguments.Count) {
            return $Arguments[$i + 1]
        }
        elseif ($argument.StartsWith("-mode=")) {
            return $argument.Substring("-mode=".Length)
        }
    }

    return "query"
}

function Invoke-PromQLProbe {
    param(
        [string]$Profile,

        [Parameter(ValueFromRemainingArguments = $true)]
        [string[]]$Arguments
    )

    if (-not (Test-Path -LiteralPath $script:PromQLProbeDir)) {
        throw "找不到 promql-probe 目录：$script:PromQLProbeDir"
    }

    if ([string]::IsNullOrWhiteSpace($Profile) -and -not (Test-Path -LiteralPath $script:PromQLCurrentProfilePath)) {
        throw "当前 PromQL profile 未设置，请先执行 Use-PromQLProfile <name>，或使用 Invoke-PromQLProbe -Profile <name>。"
    }

    $config = Read-PromQLProfileConfig
    $currentName = $Profile
    if ([string]::IsNullOrWhiteSpace($currentName)) {
        $currentName = (Get-Content -LiteralPath $script:PromQLCurrentProfilePath -Raw -Encoding UTF8).Trim()
    }
    $resolvedName = Resolve-PromQLProfile -Name $currentName -Config $config
    $profile = $config.$resolvedName
    $profileInfo = Get-PromQLProfile -Name $resolvedName

    Set-PromQLProbeEnvironment -ProfileInfo $profileInfo -RawProfile $profile

    $escapingError = Get-PromQLProbeEscapingError -Arguments $Arguments
    if ($escapingError) {
        [pscustomobject]@{
            status = "error"
            mode = Get-PromQLProbeMode -Arguments $Arguments
            datasource = $profileInfo.datasource
            error = $escapingError
        } | ConvertTo-Json -Depth 4
        return
    }

    Push-Location -LiteralPath $script:PromQLProbeDir
    try {
        & go run . @Arguments
    }
    finally {
        Pop-Location
    }
}
