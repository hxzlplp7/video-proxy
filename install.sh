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
PORT="8000"

check_root() {
    if [[ $EUID -ne 0 ]]; then
        echo -e "${CRED}错误: 本脚本必须以 root 用户运行!${CEND}"
        exit 1
    fi
}

install_app() {
    echo -e "${CBLUE}=> 正在安装 Video Proxy Server...${CEND}"
    
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
    # Use a GitHub proxy to accelerate downloads in mainland China
    GH_PROXY="https://ghproxy.cn/"
    DOWNLOAD_URL="${GH_PROXY}https://github.com/${GITHUB_REPO}/releases/latest/download/proxy-server-linux-${BIN_ARCH}"
    
    echo -e "${CYELLOW}正在从 GitHub (含中国大陆加速代理) 下载适合 ${BIN_ARCH} 架构的二进制文件...${CEND}"
    mkdir -p ${APP_DIR}
    mkdir -p ${APP_DIR}/downloads
    
    curl -L -o ${APP_DIR}/${BINARY_NAME} ${DOWNLOAD_URL}
    
    if [ ! -s "${APP_DIR}/${BINARY_NAME}" ] || grep -q "Not Found" "${APP_DIR}/${BINARY_NAME}"; then
        echo -e "${CRED}错误: 下载失败。未能从 GitHub 获取二进制文件。${CEND}"
        rm -f ${APP_DIR}/${BINARY_NAME}
        exit 1
    fi
    
    chmod +x ${APP_DIR}/${BINARY_NAME}

    # Create Systemd service
    cat > /etc/systemd/system/${SERVICE_NAME}.service << EOF
[Unit]
Description=Video Proxy Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=${APP_DIR}
ExecStart=${APP_DIR}/${BINARY_NAME}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

    # Reload systemd and start service
    systemctl daemon-reload
    systemctl enable ${SERVICE_NAME}
    systemctl start ${SERVICE_NAME}

    echo -e "${CGREEN}=> 安装成功!${CEND}"
    echo -e "代理服务已启动在后台运行，端口为: ${PORT}"
    echo -e "您可以使用以下命令管理服务:"
    echo -e "  启动: ${CYELLOW}systemctl start ${SERVICE_NAME}${CEND}"
    echo -e "  停止: ${CYELLOW}systemctl stop ${SERVICE_NAME}${CEND}"
    echo -e "  重启: ${CYELLOW}systemctl restart ${SERVICE_NAME}${CEND}"
    echo -e "  查看状态: ${CYELLOW}systemctl status ${SERVICE_NAME}${CEND}"
    echo -e "  查看日志: ${CYELLOW}journalctl -u ${SERVICE_NAME} -f${CEND}"
}

uninstall_app() {
    echo -e "${CYELLOW}=> 正在卸载 Video Proxy Server...${CEND}"
    systemctl stop ${SERVICE_NAME} 2>/dev/null
    systemctl disable ${SERVICE_NAME} 2>/dev/null
    rm -f /etc/systemd/system/${SERVICE_NAME}.service
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
        journalctl -u ${SERVICE_NAME} -f
        ;;
    0)
        exit 0
        ;;
    *)
        echo -e "${CRED}请输入正确的数字!${CEND}"
        ;;
esac
