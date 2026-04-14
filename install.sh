#!/bin/bash
# Agent Speaker 一键安装脚本

set -e

REPO="AuraAIHQ/agent-speaker"
INSTALL_DIR="/usr/local/bin"
VERSION="${VERSION:-latest}"

# 检测操作系统和架构
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    
    case "$ARCH" in
        x86_64) ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        *) echo "不支持的架构: $ARCH"; exit 1 ;;
    esac
    
    case "$OS" in
        linux|darwin) ;;  # 支持
        *) echo "不支持的操作系统: $OS"; exit 1 ;;
    esac
    
    echo "${OS}-${ARCH}"
}

# 下载并安装
install() {
    PLATFORM=$(detect_platform)
    echo "检测到平台: $PLATFORM"
    
    if [ "$VERSION" = "latest" ]; then
        DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/agent-speaker-${PLATFORM}.tar.gz"
    else
        DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/agent-speaker-${PLATFORM}.tar.gz"
    fi
    
    echo "下载地址: $DOWNLOAD_URL"
    
    # 创建临时目录
    TMP_DIR=$(mktemp -d)
    trap "rm -rf $TMP_DIR" EXIT
    
    # 下载
    echo "正在下载..."
    if command -v curl &> /dev/null; then
        curl -L -o "$TMP_DIR/agent-speaker.tar.gz" "$DOWNLOAD_URL"
    elif command -v wget &> /dev/null; then
        wget -O "$TMP_DIR/agent-speaker.tar.gz" "$DOWNLOAD_URL"
    else
        echo "需要 curl 或 wget"
        exit 1
    fi
    
    # 解压
    echo "正在解压..."
    tar -xzf "$TMP_DIR/agent-speaker.tar.gz" -C "$TMP_DIR"
    
    # 安装
    echo "正在安装到 $INSTALL_DIR..."
    if [ -w "$INSTALL_DIR" ]; then
        mv "$TMP_DIR/agent-speaker" "$INSTALL_DIR/"
        chmod +x "$INSTALL_DIR/agent-speaker"
    else
        echo "需要管理员权限，请输入密码:"
        sudo mv "$TMP_DIR/agent-speaker" "$INSTALL_DIR/"
        sudo chmod +x "$INSTALL_DIR/agent-speaker"
    fi
    
    # 验证
    if command -v agent-speaker &> /dev/null; then
        echo "✅ 安装成功!"
        agent-speaker --version
        echo ""
        echo "快速开始:"
        echo "  agent-speaker identity create --nickname alice --default"
        echo "  agent-speaker --help"
    else
        echo "⚠️ 安装可能成功，但命令未找到"
        echo "请确保 $INSTALL_DIR 在 PATH 中"
    fi
}

# 从源码编译安装
install_from_source() {
    echo "从源码编译安装..."
    
    if ! command -v go &> /dev/null; then
        echo "需要安装 Go: https://golang.org/dl/"
        exit 1
    fi
    
    if ! command -v git &> /dev/null; then
        echo "需要安装 Git"
        exit 1
    fi
    
    TMP_DIR=$(mktemp -d)
    trap "rm -rf $TMP_DIR" EXIT
    
    echo "克隆仓库..."
    git clone --depth 1 https://github.com/${REPO}.git "$TMP_DIR/repo"
    cd "$TMP_DIR/repo"
    
    echo "编译..."
    go build -o bin/agent-speaker .
    
    echo "安装..."
    if [ -w "$INSTALL_DIR" ]; then
        mv bin/agent-speaker "$INSTALL_DIR/"
    else
        sudo mv bin/agent-speaker "$INSTALL_DIR/"
    fi
    
    echo "✅ 从源码安装成功!"
}

# 主逻辑
case "${1:-}" in
    --source|-s)
        install_from_source
        ;;
    --help|-h)
        echo "Agent Speaker 安装脚本"
        echo ""
        echo "用法:"
        echo "  curl -fsSL https://.../install.sh | bash"
        echo "  curl -fsSL https://.../install.sh | bash -s -- --source"
        echo ""
        echo "选项:"
        echo "  --source   从源码编译安装"
        echo "  --help     显示帮助"
        ;;
    *)
        install
        ;;
esac
