@echo off
setlocal

set "scriptDir=%~dp0"
set "ps1=%scriptDir%install.ps1"

if not exist "%ps1%" (
    echo 错误：未找到同目录 install.ps1
    exit /b 1
)

powershell -NoProfile -ExecutionPolicy Bypass -File "%ps1%" %*
exit /b %ERRORLEVEL%
