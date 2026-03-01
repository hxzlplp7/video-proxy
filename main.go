package main

import (
	"bytes"
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
	resp, err := http.Get(targetURLStr)
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

	resp, err := http.Get(task.URL)
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
