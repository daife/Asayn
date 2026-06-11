param(
    [switch]$MigrateOnly
)

function Read-AsaynPrompt {
    param([string]$Prompt)
    try {
        Write-Host -NoNewline $Prompt
        $stream = [System.IO.File]::Open("CONIN$", [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
        try {
            $reader = New-Object System.IO.StreamReader($stream, [Console]::InputEncoding, $false, 1024, $true)
            return $reader.ReadLine()
        } finally {
            $stream.Dispose()
        }
    } catch {
        return Read-Host $Prompt
    }
}

function Get-AsaynSkillNameFromFile {
    param([string]$Path)
    try {
        $text = Get-Content -LiteralPath $Path -Raw -Encoding UTF8 -ErrorAction Stop
    } catch {
        return ""
    }
    if ($text.StartsWith("---`n")) {
        $rest = $text.Substring(4)
        $idx = $rest.IndexOf("`n---")
        if ($idx -ge 0) {
            $front = $rest.Substring(0, $idx)
            foreach ($line in ($front -split "`n")) {
                if ($line -match '^\s*name\s*:\s*(.+?)\s*$') {
                    return $Matches[1].Trim().Trim("'`"")
                }
            }
        }
    }
    return ""
}

function Get-AsaynSafeFileName {
    param([string]$Name)
    $safe = ($Name -replace '[^A-Za-z0-9._-]+', '_').Trim('._-')
    if ([string]::IsNullOrWhiteSpace($safe)) { return "mcp_server" }
    return $safe
}

function Test-AsaynPluginPath {
    param([string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return $false }
    $parts = $Path -split '[\\/]+'
    foreach ($part in $parts) {
        if ($part -eq "plugins") { return $true }
    }
    return $false
}

function Get-AsaynExistingSkillNames {
    param([string]$AsaynSkillsDir)
    $names = New-Object 'System.Collections.Generic.HashSet[string]'
    if ([string]::IsNullOrWhiteSpace($AsaynSkillsDir)) { return $names }
    if (!(Test-Path $AsaynSkillsDir)) { return $names }
    Get-ChildItem -LiteralPath $AsaynSkillsDir -Directory -ErrorAction SilentlyContinue | ForEach-Object {
        $skillFile = Join-Path $_.FullName "SKILL.md"
        if (Test-Path $skillFile) {
            $skillName = Get-AsaynSkillNameFromFile $skillFile
            if (![string]::IsNullOrWhiteSpace($skillName)) {
                [void]$names.Add($skillName)
            }
            [void]$names.Add($_.Name)
        }
    }
    return $names
}

function Get-ClaudeSkillCandidates {
    param([string]$AsaynSkillsDir)
    if ([string]::IsNullOrWhiteSpace($AsaynSkillsDir)) { return @() }
    New-Item -ItemType Directory -Path $AsaynSkillsDir -Force | Out-Null
    $roots = New-Object 'System.Collections.Generic.List[string]'
    if ($env:CLAUDE_CONFIG_DIR) {
        $roots.Add((Join-Path $env:CLAUDE_CONFIG_DIR "skills"))
    }
    $roots.Add((Join-Path $env:USERPROFILE ".claude\skills"))
    if ($env:APPDATA) {
        $roots.Add((Join-Path $env:APPDATA "Claude\skills"))
    }
    $roots.Add((Join-Path (Get-Location).Path ".claude\skills"))

    $existing = Get-AsaynExistingSkillNames $AsaynSkillsDir
    $seen = New-Object 'System.Collections.Generic.HashSet[string]'
    $items = @()
    foreach ($root in $roots) {
        if (!(Test-Path $root)) { continue }
        Get-ChildItem -LiteralPath $root -Recurse -Filter "SKILL.md" -File -ErrorAction SilentlyContinue | ForEach-Object {
            $folder = $_.Directory.FullName
            if (Test-AsaynPluginPath $folder) { return }
            $key = (Resolve-Path -LiteralPath $folder).Path
            if ($seen.Contains($key)) { return }
            [void]$seen.Add($key)
            $name = Get-AsaynSkillNameFromFile $_.FullName
            if ([string]::IsNullOrWhiteSpace($name)) { $name = $_.Directory.Name }
            $target = Join-Path $AsaynSkillsDir $_.Directory.Name
            $dup = (Test-Path $target) -or $existing.Contains($_.Directory.Name) -or $existing.Contains($name)
            $items += [pscustomobject]@{
                Kind = "skill"
                Name = $name
                FolderName = $_.Directory.Name
                Source = $folder
                Target = $target
                Duplicate = $dup
            }
        }
    }
    return $items | Sort-Object Name, Source
}

function Add-McpServersFromRoot {
    param($Object, [string]$Source, [System.Collections.Generic.List[object]]$Out)
    if ($null -eq $Object -or $Object -isnot [pscustomobject]) { return }
    $mcpProp = $Object.PSObject.Properties | Where-Object { $_.Name -eq "mcpServers" } | Select-Object -First 1
    if ($null -eq $mcpProp -or $null -eq $mcpProp.Value -or $mcpProp.Value -isnot [pscustomobject]) { return }
    foreach ($srv in $mcpProp.Value.PSObject.Properties) {
        if ($srv.Value -is [pscustomobject]) {
            $Out.Add([pscustomobject]@{ Name = [string]$srv.Name; Config = $srv.Value; Source = $Source })
        }
    }
}

function Get-AsaynExistingMcpNames {
    param([string]$AsaynMcpDir)
    $names = New-Object 'System.Collections.Generic.HashSet[string]'
    if ([string]::IsNullOrWhiteSpace($AsaynMcpDir)) { return $names }
    New-Item -ItemType Directory -Path $AsaynMcpDir -Force | Out-Null
    if (!(Test-Path $AsaynMcpDir)) { return $names }
    Get-ChildItem -LiteralPath $AsaynMcpDir -Filter "*.json" -File -ErrorAction SilentlyContinue | ForEach-Object {
        try {
            $data = Get-Content -LiteralPath $_.FullName -Raw -Encoding UTF8 | ConvertFrom-Json -ErrorAction Stop
            $mcpServers = $data.mcpServers
            if ($null -ne $mcpServers -and $mcpServers -is [pscustomobject]) {
                foreach ($srv in $mcpServers.PSObject.Properties) { [void]$names.Add([string]$srv.Name) }
            }
        } catch {}
    }
    return $names
}

function Get-ClaudeMcpCandidates {
    param([string]$AsaynMcpDir)
    $paths = New-Object 'System.Collections.Generic.List[string]'
    if ($env:CLAUDE_CONFIG_DIR) { $paths.Add($env:CLAUDE_CONFIG_DIR) }
    $paths.Add((Join-Path $env:USERPROFILE ".claude.json"))
    $paths.Add((Join-Path $env:USERPROFILE ".claude"))
    if ($env:APPDATA) { $paths.Add((Join-Path $env:APPDATA "Claude")) }
    if ($env:LOCALAPPDATA) { $paths.Add((Join-Path $env:LOCALAPPDATA "Claude")) }
    $paths.Add((Join-Path (Get-Location).Path ".mcp.json"))
    $paths.Add((Join-Path (Get-Location).Path ".claude"))

    $jsonFiles = New-Object 'System.Collections.Generic.List[string]'
    $seenFiles = New-Object 'System.Collections.Generic.HashSet[string]'
    foreach ($path in $paths) {
        if (!(Test-Path $path)) { continue }
        $candidates = @()
        if ((Get-Item -LiteralPath $path).PSIsContainer) {
            $candidates = Get-ChildItem -LiteralPath $path -Recurse -Filter "*.json" -File -ErrorAction SilentlyContinue | Where-Object { $_.Length -le 5MB -and !(Test-AsaynPluginPath $_.FullName) } | Select-Object -ExpandProperty FullName
        } elseif ($path.ToLower().EndsWith(".json")) {
            if (!(Test-AsaynPluginPath $path)) {
                $candidates = @($path)
            }
        }
        foreach ($f in $candidates) {
            try { $key = (Resolve-Path -LiteralPath $f).Path } catch { $key = $f }
            if (!$seenFiles.Contains($key)) { [void]$seenFiles.Add($key); $jsonFiles.Add($key) }
        }
    }

    $raw = New-Object 'System.Collections.Generic.List[object]'
    foreach ($file in $jsonFiles) {
        try { $data = Get-Content -LiteralPath $file -Raw -Encoding UTF8 | ConvertFrom-Json -ErrorAction Stop } catch { continue }
        Add-McpServersFromRoot $data $file $raw
    }

    $existing = Get-AsaynExistingMcpNames $AsaynMcpDir
    $byName = @{}
    foreach ($item in $raw) {
        if ($byName.ContainsKey($item.Name)) { continue }
        $safe = Get-AsaynSafeFileName $item.Name
        $target = Join-Path $AsaynMcpDir ($safe + ".json")
        $dup = (Test-Path $target) -or $existing.Contains($item.Name)
        $byName[$item.Name] = [pscustomobject]@{
            Kind = "mcp"
            Name = $item.Name
            Config = $item.Config
            Source = $item.Source
            Duplicate = $dup
        }
    }
    return $byName.Values | Sort-Object Name
}

function Convert-AsaynSelection {
    param([string]$Raw, [int[]]$DefaultIds, [int]$MaxId, [int[]]$Blocked)
    $blockedSet = @{}
    foreach ($b in $Blocked) { $blockedSet[$b] = $true }
    $rawValue = ""
    if ($null -ne $Raw) { $rawValue = $Raw.Trim().ToLower() }
    $result = New-Object 'System.Collections.Generic.HashSet[int]'
    if ($rawValue -eq "") { foreach ($i in $DefaultIds) { [void]$result.Add($i) }; return $result }
    if ($rawValue -eq "a" -or $rawValue -eq "all") {
        for ($i = 1; $i -le $MaxId; $i++) { if (!$blockedSet.ContainsKey($i)) { [void]$result.Add($i) } }
        return $result
    }
    if ($rawValue -eq "n" -or $rawValue -eq "none" -or $rawValue -eq "no") { return $result }
    foreach ($part in ($rawValue -split '[,\s]+')) {
        if ([string]::IsNullOrWhiteSpace($part)) { continue }
        if ($part -match '^(\d+)-(\d+)$') {
            $a = [int]$Matches[1]; $b = [int]$Matches[2]
            if ($a -gt $b) { $tmp = $a; $a = $b; $b = $tmp }
            for ($i = $a; $i -le $b; $i++) { if ($i -ge 1 -and $i -le $MaxId -and !$blockedSet.ContainsKey($i)) { [void]$result.Add($i) } }
        } elseif ($part -match '^\d+$') {
            $i = [int]$part
            if ($i -ge 1 -and $i -le $MaxId -and !$blockedSet.ContainsKey($i)) { [void]$result.Add($i) }
        }
    }
    return $result
}

function Invoke-AsaynClaudeMigration {
    param([switch]$SkipInitialPrompt)
    Write-Host ""
    if (!$SkipInitialPrompt) {
        $response = Read-AsaynPrompt "是否迁移 Claude Code 的 skills 和 MCP 配置？(y/N): "
        if ($response -ne "y" -and $response -ne "Y") { return }
    }

    $asaynDir = Join-Path $env:USERPROFILE ".Asayn"
    $asaynSkills = Join-Path $asaynDir "skills"
    $asaynMcp = Join-Path $asaynDir "mcp"
    New-Item -ItemType Directory -Path $asaynSkills -Force | Out-Null
    New-Item -ItemType Directory -Path $asaynMcp -Force | Out-Null

    $items = @()
    $items += Get-ClaudeSkillCandidates $asaynSkills
    $items += Get-ClaudeMcpCandidates $asaynMcp
    if ($items.Count -eq 0) {
        Write-Host "未发现可迁移的 Claude Code skills 或 MCP 配置。" -ForegroundColor Yellow
        return
    }

    Write-Host ""
    Write-Host "发现以下 Claude Code 可迁移项：" -ForegroundColor Cyan
    $indexed = @{}
    $defaultIds = @()
    $blocked = @()
    $idx = 1
    foreach ($item in $items) {
        $status = if ($item.Duplicate) { "已存在，跳过" } else { "默认迁移" }
        $type = if ($item.Kind -eq "skill") { "Skill" } else { "MCP" }
        Write-Host ("  [{0}] {1}: {2}  ({3})" -f $idx, $type, $item.Name, $status)
        Write-Host ("      来源: {0}" -f $item.Source)
        if ($item.Duplicate) { $blocked += $idx } else { $defaultIds += $idx }
        $indexed[$idx] = $item
        $idx++
    }

    Write-Host ""
    Write-Host "输入要迁移的编号，例如 1,3,5 或 2-4；a=全部可迁移；n=不迁移；直接回车=迁移全部非重复项。"
    $selected = Convert-AsaynSelection (Read-AsaynPrompt "选择: ") $defaultIds ($idx - 1) $blocked
    if ($selected.Count -eq 0) {
        Write-Host "未选择任何迁移项。"
        return
    }

    Write-Host ""
    Write-Host "将迁移："
    foreach ($i in ($selected | Sort-Object)) {
        $item = $indexed[$i]
        $type = if ($item.Kind -eq "skill") { "Skill" } else { "MCP" }
        Write-Host ("  - {0}: {1}" -f $type, $item.Name)
    }
    $confirm = Read-AsaynPrompt "确认迁移？(y/N): "
    if ($confirm -ne "y" -and $confirm -ne "Y") {
        Write-Host "已取消迁移。"
        return
    }

    $migrated = @()
    $skipped = @()
    foreach ($i in ($selected | Sort-Object)) {
        $item = $indexed[$i]
        try {
            if ($item.Kind -eq "skill") {
                if (Test-Path $item.Target) { $skipped += "skill $($item.Name)：目标目录已存在"; continue }
                Copy-Item -LiteralPath $item.Source -Destination $item.Target -Recurse -Force:$false
                $migrated += "skill $($item.Name) -> $($item.Target)"
            } else {
                $base = Get-AsaynSafeFileName $item.Name
                $target = Join-Path $asaynMcp ($base + ".json")
                if (Test-Path $target) { $skipped += "mcp $($item.Name)：同名配置已存在"; continue }
                $suffix = 1
                while (Test-Path $target) {
                    $target = Join-Path $asaynMcp ("{0}_{1}.json" -f $base, $suffix)
                    $suffix++
                }
                $serverMap = [ordered]@{}
                $serverMap[$item.Name] = $item.Config
                $obj = [ordered]@{ mcpServers = $serverMap }
                $obj | ConvertTo-Json -Depth 50 | Set-Content -LiteralPath $target -Encoding UTF8
                $migrated += "mcp $($item.Name) -> $target"
            }
        } catch {
            $skipped += "$($item.Kind) $($item.Name)：$($_.Exception.Message)"
        }
    }

    Write-Host ""
    Write-Host "迁移完成。" -ForegroundColor Green
    if ($migrated.Count -gt 0) {
        Write-Host "已迁移："
        foreach ($line in $migrated) { Write-Host "  - $line" }
    }
    if ($skipped.Count -gt 0) {
        Write-Host "已跳过："
        foreach ($line in $skipped) { Write-Host "  - $line" }
    }
}

if ($MigrateOnly) {
    Invoke-AsaynClaudeMigration -SkipInitialPrompt
    exit 0
}

# Asayn Windows 安装脚本
Write-Host "=== Asayn Windows 安装脚本 ===" -ForegroundColor Cyan
Write-Host ""

# 检查 ~/.Asayn 文件夹是否存在
$asaynDir = "$env:USERPROFILE\.Asayn"
if (Test-Path $asaynDir) {
    Write-Host "检测到已存在的 $asaynDir 文件夹。" -ForegroundColor Yellow
    $response = Read-AsaynPrompt "是否清空并重新安装？(y/N): "
    if ($response -eq "y" -or $response -eq "Y") {
        Write-Host "正在清空 $asaynDir 文件夹..."
        Remove-Item -Recurse -Force $asaynDir
        Write-Host "清空完成。"
    } else {
        Write-Host "保留现有文件夹。"
    }
}

# 获取最新版本
Write-Host "正在获取最新版本信息..." -ForegroundColor Green
try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/daife/Asayn/releases/latest"
    $latestVersion = $release.tag_name
} catch {
    Write-Host "错误：无法获取最新版本信息" -ForegroundColor Red
    exit 1
}

Write-Host "最新版本: $latestVersion" -ForegroundColor Green

# 检测系统架构
$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
Write-Host "系统架构: $arch" -ForegroundColor Green

# 创建安装目录
$installDir = "$env:USERPROFILE\.local\bin"
if (!(Test-Path $installDir)) {
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
}

# 下载二进制文件
Write-Host "正在下载 Asayn $latestVersion..." -ForegroundColor Green
$downloadUrl = "https://github.com/daife/Asayn/releases/download/$latestVersion/asayn-windows-$arch.exe"
$downloadPath = "$installDir\asayn.exe"

try {
    Invoke-WebRequest -Uri $downloadUrl -OutFile $downloadPath
} catch {
    Write-Host "错误：下载失败" -ForegroundColor Red
    exit 1
}

# 添加到 PATH（当前会话和永久性）
$currentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($currentPath -notlike "*$installDir*") {
    Write-Host "正在添加 $installDir 到 PATH..." -ForegroundColor Yellow
    
    # 更新当前会话的 PATH
    $env:PATH = "$env:PATH;$installDir"
    
    # 更新永久性 PATH（用户级别）
    $newPath = "$currentPath;$installDir"
    [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
    
    # 刷新环境变量（让当前终端立即生效）
    $env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
    
    Write-Host "PATH 已更新，当前终端已生效。" -ForegroundColor Green
}

Write-Host ""
Write-Host "=== 安装完成 ===" -ForegroundColor Cyan
Write-Host "Asayn 已安装到: $installDir\asayn.exe"
Write-Host ""
Write-Host "环境变量已自动配置，无需重启终端。" -ForegroundColor Green
Write-Host ""
Write-Host "配置文件位置:"
Write-Host "  $env:USERPROFILE\.Asayn\api_config.toml"
Write-Host "  在此文件中配置您的 API 密钥(首次运行后才会自动生成该目录)"
Write-Host ""
Invoke-AsaynClaudeMigration

Write-Host "使用方法:"
Write-Host "  cd \path\to\your\project"
Write-Host "  asayn"
