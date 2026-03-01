# Video Proxy Server 部署文档

这是一款基于 Golang 开发的高性能、单文件、无依赖的视频代理与下载服务器。它完美支持跨域视频播放、MP4/FLV 等格式的进度条拖放，并能深度解析 `.m3u8` 后将流媒体分发重写为自身节点，从而解决各类防盗链和跨域问题。

## 环境要求

本程序最大的优势在于**无需任何环境依赖**。
不管是 CentOS、Ubuntu 还是 Debian，你都**不需要装 Python, Java, Node.js 或 Go**。只需将编译好的单一可执行文件扔进服务器即可。

## 一、服务器端安装步骤 (Linux)

### 方法 1：手动运行 (测试推荐)

1. 在服务器上创建一个目录，例如 `mkdir /opt/video-proxy && cd /opt/video-proxy`
2. 将在此项目下编译好的 Linux 版本的单一可执行二进制文件（如 `proxy-server-linux`）上传到服务器并放置在该目录下。
3. 赋予它运行权限：

   ```bash
   chmod +x proxy-server-linux
   ```

4. 直接运行测试：

   ```bash
   ./proxy-server-linux
   ```

   *此时你可以看到日志输出，按 `Ctrl+C` 退出。*
5. 后台静默运行（如果不想设置开机启动）：

   ```bash
   nohup ./proxy-server-linux > output.log 2>&1 &
   ```

### 方法 2：使用提供的一键脚本安装 (生产环境推荐)

如果你希望该程序能在服务器**开机自启**并在后台稳定运行，我们提供了一个一键安装脚本 `install.sh`。

1. 确保您的二进制文件在这台主机上已重命名为 `proxy-server`。
2. 将 `proxy-server` 和本目录下的 `install.sh` 一起上传到服务器的同一个目录。
3. 在该目录下执行：

   ```bash
   chmod +x install.sh
   sudo ./install.sh
   ```

4. 菜单中输入 `1` 进行安装。安装后它将自动作为 Systemd 服务运行在默认的 `8000` 端口。

## 二、功能接口调用参数

假设您的服务器部署在国内或者国外的公网 IP `http://192.168.1.100:8000`。

### 1. 视频流代理点播 (支持防盗链 M3U8 解析及 MP4 拖放)

这是最核心的功能。将目标文件或流 URL 作为 `url` 参数发送给 `/proxy` 端点即可：

* **代理 M3U8 (系统会自动解析原始文本，重写并转发所有切片 ts)：**
    `http://192.168.1.100:8000/proxy?url=https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8`
* **代理标准点播格式 (MP4, FLV 等)：**
    `http://192.168.1.100:8000/proxy?url=https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/360/Big_Buck_Bunny_360_10s_1MB.mp4`

> 代理功能会直接流式转发回客户端，不占用服务器本地硬盘容量。

### 2. 服务器后台视频下载

如果您希望服务器自己充当离线下载机，帮您将在线资源下载到本机硬盘：

* **创建下载任务：**
    发送一个 GET 或 POST 请求到 `/download`:
    `http://192.168.1.100:8000/download?url=https://test-videos...mp4`
    服务器将立刻返回一个分配的 `task_id`，例如：`{"task_id": "task_177233682", ...}`。此刻服务已在后台静默下线此视频。

* **查询下载进度：**
    使用 `/status` 并附带刚才的 `task_id`:
    `http://192.168.1.100:8000/status?id=task_177233682`

### 3. 本地视频资源托管

一旦上述的后台下载成功完毕，文件会被存放在程序所在目录下的 `downloads/` 文件夹内。你可以直接通过类似网页播放静态文件的方式直接看：

`http://192.168.1.100:8000/local/文件名字.mp4`

## 三、二次开发与编译指南

如果您想自己重构或升级此项目源代码（例如修改端口号或防盗链逻辑），只需要了解基础的 Go 语法。

1. **项目依赖:** 在本机安装 Go，然后开启模块代理： `go env -w GOPROXY=https://goproxy.cn,direct`
2. **安装内部依赖:** 获取必要的解析库：`go get -u github.com/grafov/m3u8`
3. **本地试运行:** `go run main.go`
4. **编译目标系统的单一可执行文件:**
   在 Windows 下为 Linux 服务器编译架构（最常用）：

   ```powershell
   $env:GOOS="linux"; $env:GOARCH="amd64"
   go build -ldflags="-w -s" -o proxy-server-linux
   ```

   如果是为 Windows 平台编译，只需：

   ```powershell
   go build -ldflags="-w -s" -o proxy-server.exe
   ```
