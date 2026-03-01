package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/grafov/m3u8"
)

var (
	downloadDir   = "downloads"
	downloadTasks = make(map[string]*DownloadTask)
	taskMutex     sync.RWMutex
)

type DownloadTask struct {
	ID         string
	URL        string
	Status     string // "downloading", "completed", "error"
	Filename   string
	ErrorErr   string
	Downloaded int64
}

func init() {
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		log.Fatalf("Failed to create download directory: %v", err)
	}
}

func main() {
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/proxy", handleProxy)
	http.HandleFunc("/download", handleDownload)
	http.HandleFunc("/status", handleStatus)

	// Serve local files
	fileServer := http.FileServer(http.Dir(downloadDir))
	http.Handle("/local/", http.StripPrefix("/local/", fileServer))

	port := flag.String("port", "8000", "Port to run the proxy server on")
	flag.Parse()

	log.Printf("Starting Video Proxy Server on :%s\n", *port)
	log.Fatal(http.ListenAndServe(":"+*port, nil))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}

func handleProxy(w http.ResponseWriter, r *http.Request) {
	targetURLStr := r.URL.Query().Get("url")
	if targetURLStr == "" {
		http.Error(w, "missing 'url' parameter", http.StatusBadRequest)
		return
	}

	targetURL, err := url.Parse(targetURLStr)
	if err != nil {
		http.Error(w, "invalid 'url' parameter", http.StatusBadRequest)
		return
	}

	// Check if it's an m3u8 playlist
	if strings.HasSuffix(targetURL.Path, ".m3u8") {
		proxyM3U8(w, r, targetURLStr)
		return
	}

	// Otherwise, act as a standard transparent reverse proxy for media files
	proxyMediaFile(w, r, targetURLStr)
}

func proxyMediaFile(w http.ResponseWriter, r *http.Request, targetURL string) {
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		http.Error(w, "failed to create request", http.StatusInternalServerError)
		return
	}

	// Forward client headers (important for Range requests and User-Agent)
	copyHeaders(req.Header, r.Header)
	// Remove some headers that might cause issues
	req.Header.Del("Host")
	req.Header.Del("Connection")

	client := &http.Client{
		Timeout: 0, // No timeout for media streaming
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to fetch target: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers back to the client
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	// Stream the body
	_, err = io.Copy(w, resp.Body)
	if err != nil && err != io.EOF && !strings.Contains(err.Error(), "client disconnected") {
		log.Printf("Error streaming media to client: %v", err)
	}
}

func proxyM3U8(w http.ResponseWriter, r *http.Request, targetURLStr string) {
	req, err := http.NewRequest(http.MethodGet, targetURLStr, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create m3u8 request: %v", err), http.StatusInternalServerError)
		return
	}
	copyHeaders(req.Header, r.Header)
	req.Header.Del("Host")

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to fetch m3u8: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("target returned status %d", resp.StatusCode), http.StatusBadGateway)
		return
	}

	// Parse the playlist
	playlist, listType, err := m3u8.DecodeFrom(resp.Body, true)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to parse m3u8: %v", err), http.StatusInternalServerError)
		return
	}

	targetURL, _ := url.Parse(targetURLStr)
	baseURL := targetURL.ResolveReference(&url.URL{Path: "."})

	hostURL := fmt.Sprintf("http://%s/proxy?url=", r.Host)

	switch listType {
	case m3u8.MASTER:
		masterpl := playlist.(*m3u8.MasterPlaylist)
		for _, variant := range masterpl.Variants {
			if variant != nil {
				variantURL, _ := baseURL.Parse(variant.URI)
				variant.URI = hostURL + url.QueryEscape(variantURL.String())
			}
		}
	case m3u8.MEDIA:
		mediapl := playlist.(*m3u8.MediaPlaylist)
		for _, segment := range mediapl.Segments {
			if segment != nil {
				segmentURL, _ := baseURL.Parse(segment.URI)
				segment.URI = hostURL + url.QueryEscape(segmentURL.String())
			}
		}
	}

	// Set content type and return the modified M3U8 string
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")

	buf := new(bytes.Buffer)
	buf.WriteString(playlist.Encode().String())

	w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targetURLStr := r.URL.Query().Get("url")
	if targetURLStr == "" {
		http.Error(w, "missing 'url' parameter", http.StatusBadRequest)
		return
	}

	parsedURL, err := url.Parse(targetURLStr)
	if err != nil {
		http.Error(w, "invalid 'url' parameter", http.StatusBadRequest)
		return
	}

	filename := filepath.Base(parsedURL.Path)
	if filename == "/" || filename == "." || filename == "" {
		filename = fmt.Sprintf("download_%d.mp4", time.Now().Unix())
	} else if strings.HasSuffix(filename, ".m3u8") {
		// Download m3u8 to mp4 is complex purely in Go without ffmpeg.
		// For simplicity in this single-binary version, we will just save the raw m3u8 stream content
		// If it's just segments, it needs merging. We will return an error instructing to use standard links
		http.Error(w, "Direct downloading of m3u8 without ffmpeg is not supported in the basic single-binary version. Please download raw mp4/flv files.", http.StatusBadRequest)
		return
	}

	taskID := fmt.Sprintf("task_%d", time.Now().UnixNano())

	task := &DownloadTask{
		ID:       taskID,
		URL:      targetURLStr,
		Status:   "downloading",
		Filename: filename,
	}

	taskMutex.Lock()
	downloadTasks[taskID] = task
	taskMutex.Unlock()

	// Start background download
	go downloadFile(task)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"task_id": "%s", "status": "started", "message": "Download started in background"}`, taskID)
}

func downloadFile(task *DownloadTask) {
	filePath := filepath.Join(downloadDir, task.Filename)
	out, err := os.Create(filePath)
	if err != nil {
		updateTaskStatus(task.ID, "error", err.Error())
		return
	}
	defer out.Close()

	req, err := http.NewRequest(http.MethodGet, task.URL, nil)
	if err != nil {
		updateTaskStatus(task.ID, "error", err.Error())
		return
	}
	// Add a common User-Agent for downloads just in case
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		updateTaskStatus(task.ID, "error", err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		updateTaskStatus(task.ID, "error", fmt.Sprintf("server returned status: %d", resp.StatusCode))
		return
	}

	// Copy body to file and track progress (basic)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
			taskMutex.Lock()
			task.Downloaded += int64(n)
			taskMutex.Unlock()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			updateTaskStatus(task.ID, "error", err.Error())
			return
		}
	}

	updateTaskStatus(task.ID, "completed", "")
}

func updateTaskStatus(id, status, errStr string) {
	taskMutex.Lock()
	defer taskMutex.Unlock()
	if task, ok := downloadTasks[id]; ok {
		task.Status = status
		task.ErrorErr = errStr
	}
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		http.Error(w, "missing 'id' parameter", http.StatusBadRequest)
		return
	}

	taskMutex.RLock()
	task, ok := downloadTasks[taskID]
	taskMutex.RUnlock()

	if !ok {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if task.Status == "error" {
		fmt.Fprintf(w, `{"id": "%s", "status": "%s", "error": "%s"}`, task.ID, task.Status, task.ErrorErr)
	} else {
		fmt.Fprintf(w, `{"id": "%s", "status": "%s", "downloaded_bytes": %d, "filename": "%s"}`, task.ID, task.Status, task.Downloaded, task.Filename)
	}
}

const indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Video Proxy Server - Web Console</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <style>
        body {
            background: linear-gradient(135deg, #1e3c72 0%, #2a5298 100%);
            color: #ffffff;
            font-family: 'Inter', system-ui, sans-serif;
            min-height: 100vh;
        }
        .glass {
            background: rgba(255, 255, 255, 0.1);
            backdrop-filter: blur(12px);
            -webkit-backdrop-filter: blur(12px);
            border: 1px solid rgba(255, 255, 255, 0.2);
        }
        .shimmer {
            background: linear-gradient(90deg, rgba(255,255,255,0) 0%, rgba(255,255,255,0.2) 50%, rgba(255,255,255,0) 100%);
            background-size: 200% 100%;
            animation: shimmer 2s infinite linear;
        }
        @keyframes shimmer {
            0% { background-position: -200% 0; }
            100% { background-position: 200% 0; }
        }
    </style>
</head>
<body class="flex items-center justify-center p-4">
    <div class="glass max-w-2xl w-full rounded-3xl shadow-2xl p-8 sm:p-10 transition-all duration-300">
        <div class="text-center mb-10">
            <h1 class="text-4xl sm:text-5xl font-extrabold tracking-tight mb-3 text-transparent bg-clip-text bg-gradient-to-r from-teal-300 to-cyan-300">
                 Video Proxy
            </h1>
            <p class="text-blue-100 text-sm sm:text-base font-light opacity-90">
                无缝穿透防盗链，云端高速下载与流媒体代理
            </p>
        </div>

        <div class="space-y-6">
            <div class="relative group">
                <input type="text" id="videoUrl" placeholder="输入视频的 M3U8 / MP4 链接..." 
                    class="w-full px-5 py-4 rounded-xl bg-black/20 border border-white/20 text-white placeholder-blue-200/50 
                    focus:outline-none focus:ring-2 focus:ring-cyan-400 focus:bg-black/30 transition-all duration-300 shadow-inner text-lg">
                <div class="absolute inset-0 rounded-xl shimmer opacity-0 group-focus-within:opacity-100 pointer-events-none transition-opacity"></div>
            </div>

            <div class="flex flex-col sm:flex-row gap-4 pt-2">
                <button onclick="proxyPlay()" class="group relative flex-1 bg-gradient-to-r from-cyan-500 to-blue-600 hover:from-cyan-400 hover:to-blue-500 text-white font-bold py-4 px-6 rounded-xl shadow-lg transform transition-all active:scale-95 duration-200 overflow-hidden">
                    <span class="relative z-10 flex items-center justify-center gap-2">
                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"></path><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path></svg>
                        代理播放
                    </span>
                    <div class="absolute inset-0 h-full w-full bg-white/20 scale-x-0 group-hover:scale-x-100 transform origin-left transition-transform duration-300 ease-out"></div>
                </button>

                <button onclick="startDownload()" class="group relative flex-1 bg-gradient-to-r from-purple-500 to-pink-600 hover:from-purple-400 hover:to-pink-500 text-white font-bold py-4 px-6 rounded-xl shadow-lg transform transition-all active:scale-95 duration-200 overflow-hidden">
                    <span class="relative z-10 flex items-center justify-center gap-2">
                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path></svg>
                        云端本地下载
                    </span>
                    <div class="absolute inset-0 h-full w-full bg-white/20 scale-x-0 group-hover:scale-x-100 transform origin-left transition-transform duration-300 ease-out"></div>
                </button>
            </div>
            
            <div id="statusContainer" class="hidden animate-fade-in-up mt-6 overflow-hidden rounded-xl glass border border-white/10">
                <div class="bg-black/40 px-5 py-3 border-b border-white/5 flex items-center justify-between">
                    <h3 class="text-cyan-300 font-semibold text-sm tracking-wider">执行回显</h3>
                    <div class="flex gap-1.5">
                        <div class="w-2.5 h-2.5 rounded-full bg-red-400"></div>
                        <div class="w-2.5 h-2.5 rounded-full bg-yellow-400"></div>
                        <div class="w-2.5 h-2.5 rounded-full bg-green-400"></div>
                    </div>
                </div>
                <div class="p-5 font-mono text-sm shadow-inner">
                    <p id="statusMsg" class="text-green-300 break-words leading-relaxed whitespace-pre-wrap">Waiting for command...</p>
                </div>
            </div>
        </div>

        <div class="mt-8 pt-6 border-t border-white/10 flex justify-center">
            <a href="/local/" target="_blank" class="inline-flex items-center justify-center gap-2 px-6 py-3 rounded-full bg-white/5 hover:bg-white/10 border border-white/10 hover:border-cyan-300/50 text-blue-200 hover:text-white transition-all duration-300 font-medium group">
                <svg class="w-5 h-5 text-cyan-400 group-hover:-translate-y-1 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"></path></svg>
                浏览已缓存的媒体库
            </a>
        </div>
    </div>

    <style>
        @keyframes fadeInUp { from { opacity: 0; transform: translateY(10px); } to { opacity: 1; transform: translateY(0); } }
        .animate-fade-in-up { animation: fadeInUp 0.4s ease-out forwards; }
    </style>

    <script>
        function getUrl() { return document.getElementById('videoUrl').value.trim(); }
        const statusContainer = document.getElementById('statusContainer');
        const statusMsg = document.getElementById('statusMsg');

        function printLog(msg, type = 'info') {
            statusContainer.classList.remove('hidden');
            let color = 'text-green-300';
            if (type === 'error') color = 'text-red-400';
            if (type === 'warn') color = 'text-yellow-300';
            
            statusMsg.className = "break-words leading-relaxed whitespace-pre-wrap " + color;
            statusMsg.textContent = "> " + msg;
        }

        function proxyPlay() {
            const url = getUrl();
            if (!url) return printLog('错误：请先输入有效的视频 URL', 'error');
            printLog('正在打开代理流播放器...');
            window.open('/proxy?url=' + encodeURIComponent(url), '_blank');
        }

        async function startDownload() {
            const url = getUrl();
            if (!url) return printLog('错误：请先输入有效的视频 URL', 'error');
            
            printLog('正在发起后台下载请求...', 'warn');
            
            try {
                const res = await fetch('/download?url=' + encodeURIComponent(url));
                const data = await res.json().catch(() => null);
                if(res.ok && data) {
                    let log = '✅ 下载进程已启动！\n';
                    log += '任务 ID: ' + data.task_id + '\n';
                    log += '状态: ' + data.status + '\n';
                    log += '您可以随后调用 API查询进度:\n/status?id=' + data.task_id;
                    printLog(log, 'info');
                } else {
                    printLog('请求被拒绝: ' + (data?.error || "未知后端错误"), 'error');
                }
            } catch (err) {
                printLog('致命错误: ' + err.message, 'error');
            }
        }
    </script>
</body>
</html>`
