@echo off
setlocal enabledelayedexpansion

echo === Asayn Windows 安装脚本 ===
echo.

REM 检查 ~/.Asayn 文件夹是否存在
set "asaynDir=%USERPROFILE%\.Asayn"
if exist "%asaynDir%" (
    echo 检测到已存在的 %asaynDir% 文件夹。
    set /p "response=是否清空并重新安装？(y/N): "
    if /i "!response!"=="y" (
        echo 正在清空 %asaynDir% 文件夹...
        rmdir /s /q "%asaynDir%"
        echo 清空完成。
    ) else (
        echo 保留现有文件夹。
    )
)

echo 正在获取最新版本信息...
for /f "tokens=2 delims=:," %%a in ('curl -s https://api.github.com/repos/daife/Asayn/releases/latest ^| findstr "tag_name"') do (
    set "latestVersion=%%~a"
    set "latestVersion=!latestVersion:~1,-1!"
)

if "!latestVersion!"=="" (
    echo 错误：无法获取最新版本信息
    exit /b 1
)

echo 最新版本: !latestVersion!

REM 检测系统架构
if "%PROCESSOR_ARCHITECTURE%"=="AMD64" (
    set "arch=amd64"
) else (
    set "arch=386"
)

echo 系统架构: !arch!

REM 创建安装目录
set "installDir=%USERPROFILE%\.local\bin"
if not exist "%installDir%" mkdir "%installDir%"

echo 正在下载 Asayn !latestVersion!...
set "downloadUrl=https://github.com/daife/Asayn/releases/download/!latestVersion!/asayn-windows-!arch!.exe"
set "downloadPath=%installDir%\asayn.exe"

curl -L -o "%downloadPath%" "%downloadUrl%"
if errorlevel 1 (
    echo 错误：下载失败
    exit /b 1
)

REM 添加到 PATH
set "currentPath=%PATH%"
if not "!currentPath!"=="!currentPath:%installDir%=!" (
    echo %installDir% 已在 PATH 中。
) else (
    echo 正在添加 %installDir% 到 PATH...
    setx PATH "%PATH%;%installDir%"
)

echo.
echo === 安装完成 ===
echo Asayn 已安装到: %installDir%\asayn.exe
echo.
echo 请重启终端使 PATH 生效。
echo.
echo 配置文件位置:
echo   %USERPROFILE%\.Asayn\api_config.toml
echo   在此文件中配置您的 API 密钥
echo.
echo 使用方法:
echo   cd \path\to\your\project
echo   asayn

pause
