#!/bin/bash

# ==========================================
# Video Proxy Server Installation Script
# ==========================================

CEND="\033[0m"
CGREEN="\033[1;32m"
CYELLOW="\033[1;33m"
CRED="\033[1;31m"
CBLUE="\033[1;36m"

BINARY_NAME="proxy-server"
APP_DIR="/opt/video-proxy"
SERVICE_NAME="video-proxy"

check_root() {
    if [[ $EUID -ne 0 ]]; then
        echo -e "${CRED}错误: 本脚本必须以 root 用户运行!${CEND}"
        exit 1
    fi
}

check_ffmpeg() {
    if ! command -v ffmpeg >/dev/null 2>&1; then
        echo -e "${CYELLOW}检测到系统中缺少 FFmpeg (用于 M3U8 转码下载)...${CEND}"
        echo -e "${CBLUE}=> 正在尝试自动安装静态 FFmpeg...${CEND}"
        
        # Determine FFmpeg download URL based on architecture
        FF_ARCH=""
        case "$(uname -m)" in
            "x86_64"|"amd64") FF_ARCH="linux64" ;;
            "aarch64"|"arm64") FF_ARCH="linuxarm64" ;;
            *) echo -e "${CYELLOW}无法为您的架构自动安装 FFmpeg，请手动安装。${CEND}"; return ;;
        esac

        FF_TMP="/tmp/ffmpeg_install"
        mkdir -p $FF_TMP
        
        # Using BtbN/FFmpeg-Builds which is reliable and on GitHub (respects GH_PROXY)
        FF_URL="${PROXY_PREFIX}https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-${FF_ARCH}-gpl.tar.xz"
        
        echo -e "${CYELLOW}正在从 ${FF_URL} 下载 FFmpeg...${CEND}"
        if curl -L -f -o "$FF_TMP/ffmpeg.tar.xz" "$FF_URL"; then
            echo -e "${CYELLOW}正在解压并安装 FFmpeg...${CEND}"
            tar -xJf "$FF_TMP/ffmpeg.tar.xz" -C "$FF_TMP"
            # Find the ffmpeg binary in the unpacked directory
            FF_BIN=$(find "$FF_TMP" -name ffmpeg -type f | head -n 1)
            if [ -f "$FF_BIN" ]; then
                cp "$FF_BIN" /usr/local/bin/ffmpeg
                chmod +x /usr/local/bin/ffmpeg
                echo -e "${CGREEN}FFmpeg 安装成功!${CEND}"
            else
                echo -e "${CRED}错误: 解压后未找到 ffmpeg 二进制文件。${CEND}"
            fi
        else
            echo -e "${CRED}错误: 下载 FFmpeg 失败。请尝试手动安装。${CEND}"
        fi
        rm -rf "$FF_TMP"
    else
        echo -e "${CGREEN}检测到系统已安装 FFmpeg。${CEND}"
    fi
}

install_app() {
    echo -e "${CBLUE}=> 正在安装/更新 Video Proxy Server...${CEND}"
    
    # Stop existing service if any
    if systemctl is-active --quiet ${SERVICE_NAME}; then
        echo -e "${CYELLOW}检测到旧服务正在运行，正在停止...${CEND}"
        systemctl stop ${SERVICE_NAME}
    fi
    killall -9 ${BINARY_NAME} 2>/dev/null
    
    # Check architecture
    ARCH=$(uname -m)
    case "$ARCH" in
        "x86_64"|"amd64")
            BIN_ARCH="amd64"
            ;;
        "aarch64"|"arm64")
            BIN_ARCH="arm64"
            ;;
        "armv7l"|"armv7")
            BIN_ARCH="armv7"
            ;;
        *)
            echo -e "${CRED}错误: 不支持的架构: $ARCH${CEND}"
            exit 1
            ;;
    esac

    # Download latest release binary
    GITHUB_REPO="hxzlplp7/video-proxy"
    # Use GH_PROXY if provided, otherwise default to empty
    PROXY_PREFIX="${GH_PROXY}"
    DOWNLOAD_URL="${PROXY_PREFIX}https://github.com/${GITHUB_REPO}/releases/latest/download/proxy-server-linux-${BIN_ARCH}"
    
    if [ -n "$GH_PROXY" ]; then
        echo -e "${CYELLOW}正在通过代理 (${GH_PROXY}) 下载适合 ${BIN_ARCH} 架构的二进制文件...${CEND}"
    else
        echo -e "${CYELLOW}正在下载适合 ${BIN_ARCH} 架构的二进制文件...${CEND}"
    fi

    mkdir -p ${APP_DIR}
    mkdir -p ${APP_DIR}/downloads
    
    # 强制删除旧文件，防止 Text file busy
    rm -f ${APP_DIR}/${BINARY_NAME}
    
    curl -L --connect-timeout 15 -f -o ${APP_DIR}/${BINARY_NAME} ${DOWNLOAD_URL}
    
    if [ ! -s "${APP_DIR}/${BINARY_NAME}" ]; then
        echo -e "${CRED}错误: 下载失败。未能从 GitHub 获取二进制文件 (文件为空或请求超) 。${CEND}"
        rm -f ${APP_DIR}/${BINARY_NAME}
        exit 1
    fi
    
    chmod +x ${APP_DIR}/${BINARY_NAME}

    # Ask for port (handle piped bash execution)
    PORT=8000
    if [ -c /dev/tty ]; then
        read -p "请输入要监听的端口 [默认 8000]: " PORT_INPUT </dev/tty
    else
        read -p "请输入要监听的端口 [默认 8000]: " PORT_INPUT
    fi
    PORT=${PORT_INPUT:-8000}
    # Validate it's a number
    if ! [[ "$PORT" =~ ^[0-9]+$ ]]; then
        PORT=8000
    fi

    # Create Systemd service
    cat > /etc/systemd/system/${SERVICE_NAME}.service << EOF
[Unit]
Description=Video Proxy Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=${APP_DIR}
ExecStart=${APP_DIR}/${BINARY_NAME} -port ${PORT}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

    # Create command shortcut
    echo -e "${CYELLOW}正在生成全局管理菜单命令...${CEND}"
    if [ -f "$0" ] && [[ "$0" == *"install.sh" ]]; then
        # If running from local file
        cp "$0" /usr/local/bin/video-proxy
    else
        # If piped, try download with timeout
        curl -fsSL --connect-timeout 10 "${PROXY_PREFIX}https://raw.githubusercontent.com/hxzlplp7/video-proxy/main/install.sh" -o /usr/local/bin/video-proxy || echo -e "${CYELLOW}警告: 快捷菜单下载失败，您可以稍后手动设置。${CEND}"
    fi
    chmod +x /usr/local/bin/video-proxy 2>/dev/null

    # Reload systemd and start service
    systemctl daemon-reload
    systemctl enable ${SERVICE_NAME}
    systemctl start ${SERVICE_NAME}

    # Check and install FFmpeg if needed
    check_ffmpeg

    echo -e "${CGREEN}=> 安装成功!${CEND}"
    echo -e "代理服务已启动在后台运行，端口为: ${PORT}"
    echo -e "您可以使用以下命令管理服务:"
    echo -e "  启动: ${CYELLOW}systemctl start ${SERVICE_NAME}${CEND}"
    echo -e "  停止: ${CYELLOW}systemctl stop ${SERVICE_NAME}${CEND}"
    echo -e "  重启: ${CYELLOW}systemctl restart ${SERVICE_NAME}${CEND}"
    echo -e "  查看状态: ${CYELLOW}systemctl status ${SERVICE_NAME}${CEND}"
    echo -e "  查看日志: ${CYELLOW}journalctl -u ${SERVICE_NAME} -f${CEND}"
    echo -e "  管理面板: 随时输入 ${CGREEN}video-proxy${CEND} 呼出此菜单"
}

uninstall_app() {
    echo -e "${CYELLOW}=> 正在卸载 Video Proxy Server...${CEND}"
    systemctl stop ${SERVICE_NAME} 2>/dev/null
    systemctl disable ${SERVICE_NAME} 2>/dev/null
    rm -f /etc/systemd/system/${SERVICE_NAME}.service
    rm -f /usr/local/bin/video-proxy
    systemctl daemon-reload
    rm -rf ${APP_DIR}
    echo -e "${CGREEN}=> 卸载完成!${CEND}"
}

echo -e "${CBLUE}==========================================${CEND}"
echo -e "${CBLUE}      Video Proxy Server 一键管理脚本      ${CEND}"
echo -e "${CBLUE}==========================================${CEND}"
echo "1. 安装服务端"
echo "2. 卸载服务端"
echo "3. 重启服务"
echo "4. 查看实时日志"
echo "0. 退出"
echo "------------------------------------------"
read -p "请输入数字 [0-4]: " num

case "$num" in
    1)
        check_root
        install_app
        ;;
    2)
        check_root
        uninstall_app
        ;;
    3)
        check_root
        systemctl restart ${SERVICE_NAME}
        echo -e "${CGREEN}=> 服务已重启!${CEND}"
        ;;
    4)
        journalctl -u ${SERVICE_NAME} -n 50 -f
        ;;
    0)
        exit 0
        ;;
    *)
        echo -e "${CRED}请输入正确的数字!${CEND}"
        ;;
esac
