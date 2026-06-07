# Asayn Windows 安装脚本
Write-Host "=== Asayn Windows 安装脚本 ===" -ForegroundColor Cyan
Write-Host ""

# 检查 ~/.Asayn 文件夹是否存在
$asaynDir = "$env:USERPROFILE\.Asayn"
if (Test-Path $asaynDir) {
    Write-Host "检测到已存在的 $asaynDir 文件夹。" -ForegroundColor Yellow
    $response = Read-Host "是否清空并重新安装？(y/N)"
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

# 添加到 PATH
$currentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($currentPath -notlike "*$installDir*") {
    Write-Host "正在添加 $installDir 到 PATH..." -ForegroundColor Yellow
    $newPath = "$currentPath;$installDir"
    [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
    $env:PATH = "$env:PATH;$installDir"
}

Write-Host ""
Write-Host "=== 安装完成 ===" -ForegroundColor Cyan
Write-Host "Asayn 已安装到: $installDir\asayn.exe"
Write-Host ""
Write-Host "请重启终端或运行以下命令使 PATH 生效:"
Write-Host "  `$env:PATH = `"`$env:PATH;$installDir`""
Write-Host ""
Write-Host "配置文件位置:"
Write-Host "  $env:USERPROFILE\.Asayn\api_config.toml"
Write-Host "  在此文件中配置您的 API 密钥"
Write-Host ""
Write-Host "使用方法:"
Write-Host "  cd \path\to\your\project"
Write-Host "  asayn"
