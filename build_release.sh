#!/bin/bash
set -e

echo "=== Asayn Release Builder ==="
echo ""

# 获取版本号
if [ -z "$1" ]; then
    read -p "请输入版本号 (例如 v1.0.0): " VERSION
else
    VERSION=$1
fi

if [ -z "$VERSION" ]; then
    echo "错误：必须提供版本号"
    exit 1
fi

echo "构建版本: $VERSION"
echo ""

# 构建所有平台
echo "正在构建 Linux amd64..."
GOOS=linux GOARCH=amd64 go build -o "asayn-linux-amd64" ./cmd/asayn

echo "正在构建 Linux arm64..."
GOOS=linux GOARCH=arm64 go build -o "asayn-linux-arm64" ./cmd/asayn

echo "正在构建 macOS amd64..."
GOOS=darwin GOARCH=amd64 go build -o "asayn-darwin-amd64" ./cmd/asayn

echo "正在构建 macOS arm64..."
GOOS=darwin GOARCH=arm64 go build -o "asayn-darwin-arm64" ./cmd/asayn

echo "正在构建 Windows amd64..."
GOOS=windows GOARCH=amd64 go build -o "asayn-windows-amd64.exe" ./cmd/asayn

# 生成校验和
echo "正在生成校验和..."
sha256sum asayn-* > SHA256SUMS.txt

echo ""
echo "=== 构建完成 ==="
echo "已生成以下文件:"
ls -la asayn-* SHA256SUMS.txt
echo ""
echo "请将这些文件上传到 GitHub Release: $VERSION"
