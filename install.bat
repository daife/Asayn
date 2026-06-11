@echo off
setlocal

set "ps1=%TEMP%\asayn-install.ps1"
echo 正在下载 PowerShell 安装脚本...
powershell -NoProfile -ExecutionPolicy Bypass -Command "Invoke-WebRequest -Uri 'https://raw.githubusercontent.com/daife/Asayn/main/install.ps1' -OutFile (Join-Path $env:TEMP 'asayn-install.ps1')"
if not exist "%ps1%" (
    echo 错误：下载 install.ps1 失败
    exit /b 1
)

powershell -NoProfile -ExecutionPolicy Bypass -File "%ps1%"
exit /b %ERRORLEVEL%
