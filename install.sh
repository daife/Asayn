#!/bin/bash
set -e

# Asayn 安装脚本
echo "=== Asayn 安装脚本 ==="
echo ""

# 检查 ~/.Asayn 文件夹是否存在
if [ -d "$HOME/.Asayn" ]; then
    echo "检测到已存在的 ~/.Asayn 文件夹。"
    read -p "是否清空并重新安装？(y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo "正在清空 ~/.Asayn 文件夹..."
        rm -rf "$HOME/.Asayn"
        echo "清空完成。"
    else
        echo "保留现有文件夹。"
    fi
fi

# 获取最新版本
echo "正在获取最新版本信息..."
LATEST_VERSION=$(curl -s https://api.github.com/repos/daife/Asayn/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_VERSION" ]; then
    echo "错误：无法获取最新版本信息"
    exit 1
fi

echo "最新版本: $LATEST_VERSION"

# 检测系统架构
ARCH=$(uname -m)
case $ARCH in
    x86_64)
        ARCH="amd64"
        ;;
    aarch64|arm64)
        ARCH="arm64"
        ;;
    *)
        echo "不支持的架构: $ARCH"
        exit 1
        ;;
esac

echo "系统架构: $ARCH"

# 创建安装目录
INSTALL_DIR="$HOME/.local/bin"
mkdir -p "$INSTALL_DIR"

# 下载二进制文件
echo "正在下载 Asayn $LATEST_VERSION..."
DOWNLOAD_URL="https://github.com/daife/Asayn/releases/download/$LATEST_VERSION/asayn-linux-$ARCH"
curl -L -o "$INSTALL_DIR/asayn" "$DOWNLOAD_URL"

# 设置可执行权限
chmod +x "$INSTALL_DIR/asayn"

# 检查 PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo "正在添加 $INSTALL_DIR 到 PATH..."
    echo "export PATH=\"\$HOME/.local/bin:\$PATH\"" >> "$HOME/.bashrc"
    export PATH="$HOME/.local/bin:$PATH"
fi

echo ""
echo "=== 安装完成 ==="
echo "Asayn 已安装到: $INSTALL_DIR/asayn"
echo ""
echo "请运行以下命令使 PATH 生效:"
echo "  source ~/.bashrc"
echo ""
echo "配置文件位置:"
echo "  ~/.Asayn/api_config.toml"
echo "  在此文件中配置您的 API 密钥"
echo ""
echo "使用方法:"
echo "  cd /path/to/your/project"
echo "  asayn"
