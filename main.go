package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var version = "dev"

const (
	defaultPort   = "9090"
	discoveryPort = "9090"
	maxItems      = 64
	maxFileSize   = 100 << 20
)

// --- config ---

type config struct {
	Key string `json:"key,omitempty"`
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "toss")
}

func configPath() string { return filepath.Join(configDir(), "config.json") }

func loadConfig() config {
	var cfg config
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	json.Unmarshal(data, &cfg)
	return cfg
}

func saveConfig(cfg config) error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(configPath(), data, 0600)
}

func getKey() string {
	if k := os.Getenv("TOSS_KEY"); k != "" {
		return k
	}
	return loadConfig().Key
}

func configure() {
	cfg := loadConfig()
	if cfg.Key != "" {
		fmt.Println("current: key is set")
	} else {
		fmt.Println("current: no key")
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("enter shared key (leave empty to remove): ")
	key, _ := reader.ReadString('\n')
	key = strings.TrimSpace(key)

	cfg.Key = key
	if err := saveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if key == "" {
		fmt.Println("key removed — auth disabled")
	} else {
		fmt.Printf("saved to %s\n", configPath())
		fmt.Println("use the same key on all your devices.")
	}
}

// --- store ---

type item struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	Content   string    `json:"content,omitempty"`
	Filename  string    `json:"filename,omitempty"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

type store struct {
	mu      sync.RWMutex
	items   []item
	nextID  int
	dataDir string
}

var s *store

func newStore() (*store, error) {
	dir, err := os.MkdirTemp("", "toss-*")
	if err != nil {
		return nil, err
	}
	return &store{dataDir: dir, items: make([]item, 0), nextID: 1}, nil
}

func (s *store) add(it item) item {
	s.mu.Lock()
	defer s.mu.Unlock()
	it.ID = s.nextID
	s.nextID++
	it.CreatedAt = time.Now()
	s.items = append(s.items, it)
	if len(s.items) > maxItems {
		old := s.items[0]
		if old.Type == "file" {
			os.Remove(filepath.Join(s.dataDir, strconv.Itoa(old.ID)))
		}
		s.items = s.items[1:]
	}
	return it
}

func (s *store) list() []item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]item, len(s.items))
	copy(out, s.items)
	return out
}

func (s *store) latest() *item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.items) == 0 {
		return nil
	}
	it := s.items[len(s.items)-1]
	return &it
}

func (s *store) get(id int) *item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.items {
		if s.items[i].ID == id {
			it := s.items[i]
			return &it
		}
	}
	return nil
}

func (s *store) filePath(id int) string {
	return filepath.Join(s.dataDir, strconv.Itoa(id))
}

func (s *store) cleanup() { os.RemoveAll(s.dataDir) }

// --- discovery ---

func startDiscovery(httpPort string) {
	addr, err := net.ResolveUDPAddr("udp4", ":"+discoveryPort)
	if err != nil {
		return
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return
	}
	defer conn.Close()
	buf := make([]byte, 16)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		if string(buf[:n]) == "TOSS?" {
			conn.WriteToUDP([]byte("TOSS:"+httpPort), remote)
		}
	}
}

func discover() string {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))

	dst, _ := net.ResolveUDPAddr("udp4", "255.255.255.255:"+discoveryPort)
	conn.WriteTo([]byte("TOSS?"), dst)

	buf := make([]byte, 32)
	n, addr, err := conn.ReadFrom(buf)
	if err != nil {
		return ""
	}
	msg := string(buf[:n])
	if !strings.HasPrefix(msg, "TOSS:") {
		return ""
	}
	port := strings.TrimPrefix(msg, "TOSS:")
	host, _, _ := net.SplitHostPort(addr.String())
	return host + ":" + port
}

// --- service ---

func servicePath() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", "com.toss.server.plist")
	case "linux":
		return filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user", "toss.service")
	default:
		return ""
	}
}

func serviceInstall() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	switch runtime.GOOS {
	case "darwin":
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.toss.server</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/toss.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/toss.log</string>
</dict>
</plist>`, exe)
		path := servicePath()
		dir := filepath.Dir(path)
		os.MkdirAll(dir, 0755)
		if err := os.WriteFile(path, []byte(plist), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		cmd := exec.Command("launchctl", "load", path)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "error starting service: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("installed: %s\n", path)
		fmt.Println("toss server will start on login")

	case "linux":
		unit := fmt.Sprintf(`[Unit]
Description=toss server
After=network.target

[Service]
ExecStart=%s serve
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, exe)
		path := servicePath()
		dir := filepath.Dir(path)
		os.MkdirAll(dir, 0755)
		if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		exec.Command("systemctl", "--user", "daemon-reload").Run()
		cmd := exec.Command("systemctl", "--user", "enable", "--now", "toss")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "error starting service: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("installed: %s\n", path)
		fmt.Println("toss server will start on login")

	default:
		fmt.Fprintf(os.Stderr, "service install not supported on %s\n", runtime.GOOS)
		os.Exit(1)
	}
}

func serviceUninstall() {
	path := servicePath()
	if path == "" {
		fmt.Fprintf(os.Stderr, "service not supported on %s\n", runtime.GOOS)
		os.Exit(1)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Println("no service installed")
		return
	}
	switch runtime.GOOS {
	case "darwin":
		exec.Command("launchctl", "unload", path).Run()
	case "linux":
		exec.Command("systemctl", "--user", "disable", "--now", "toss").Run()
	}
	os.Remove(path)
	if runtime.GOOS == "linux" {
		exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	fmt.Println("service removed")
}

// --- auto-serve ---

func autoServe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return ""
	}
	cmd := exec.Command(exe, "serve")
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	if err := cmd.Start(); err != nil {
		devnull.Close()
		return ""
	}
	devnull.Close()
	go cmd.Wait()

	for i := 0; i < 10; i++ {
		time.Sleep(200 * time.Millisecond)
		if found := discover(); found != "" {
			return found
		}
	}
	return ""
}

// --- auth middleware ---

func withAuth(next http.Handler) http.Handler {
	key := getKey()
	if key == "" {
		return next
	}
	expected := []byte("Bearer " + key)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(provided, expected) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- HTTP handlers ---

func handleItems(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.list())
}

func handleText(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", 500)
		return
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		http.Error(w, "empty text", 400)
		return
	}
	it := s.add(item{Type: "text", Content: text, Size: int64(len(text))})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(it)
}

func handleFile(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(maxFileSize)
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file required", 400)
		return
	}
	defer file.Close()

	it := s.add(item{Type: "file", Filename: header.Filename, Size: header.Size})
	dst, err := os.Create(s.filePath(it.ID))
	if err != nil {
		http.Error(w, "storage error", 500)
		return
	}
	defer dst.Close()
	io.Copy(dst, file)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(it)
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	it := s.get(id)
	if it == nil || it.Type != "file" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, it.Filename))
	http.ServeFile(w, r, s.filePath(id))
}

func handleLatest(w http.ResponseWriter, r *http.Request) {
	it := s.latest()
	if it == nil {
		http.Error(w, "no items", 404)
		return
	}
	if it.Type == "file" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, it.Filename))
		http.ServeFile(w, r, s.filePath(it.ID))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, it.Content)
}

// --- server ---

func localIPs() []string {
	var ips []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			ips = append(ips, ipnet.IP.String())
		}
	}
	return ips
}

func serve() {
	port := defaultPort
	if p := os.Getenv("TOSS_PORT"); p != "" {
		port = p
	}

	var err error
	s, err = newStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		s.cleanup()
		fmt.Println("\nbye!")
		os.Exit(0)
	}()

	go startDiscovery(port)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/items", handleItems)
	mux.HandleFunc("POST /api/text", handleText)
	mux.HandleFunc("POST /api/file", handleFile)
	mux.HandleFunc("GET /api/download/{id}", handleDownload)
	mux.HandleFunc("GET /api/latest", handleLatest)

	fmt.Printf("toss running on :%s\n\n", port)
	for _, ip := range localIPs() {
		fmt.Printf("  http://%s:%s\n", ip, port)
	}
	if key := getKey(); key != "" {
		fmt.Println("\nauth: enabled")
	} else {
		fmt.Println("\nauth: disabled (run toss config to set a key)")
	}
	fmt.Println("auto-discovery enabled")
	fmt.Printf("or set manually: export TOSS_HOST=<ip>:%s\n\n", port)

	if err := http.ListenAndServe(":"+port, withAuth(mux)); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// --- HTTP client ---

func doReq(method, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if key := getKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return http.DefaultClient.Do(req)
}

func checkResp(resp *http.Response) {
	if resp.StatusCode == 401 {
		fmt.Fprintln(os.Stderr, "error: wrong key — run toss config")
		os.Exit(1)
	}
}

// --- clipboard ---

func clipRead() (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbpaste")
	case "linux":
		if p, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command(p, "-selection", "clipboard", "-o")
		} else if p, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command(p, "--clipboard", "--output")
		} else {
			return "", fmt.Errorf("install xclip or xsel")
		}
	case "windows":
		cmd = exec.Command("powershell", "-command", "Get-Clipboard")
	default:
		return "", fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func clipWrite(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		if p, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command(p, "-selection", "clipboard")
		} else if p, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command(p, "--clipboard", "--input")
		} else {
			return fmt.Errorf("install xclip or xsel")
		}
	case "windows":
		cmd = exec.Command("clip")
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func paste() {
	text, err := clipRead()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if text == "" {
		fmt.Fprintln(os.Stderr, "clipboard is empty")
		os.Exit(1)
	}
	sendText(text)
}

func copyLatest() {
	resp, err := doReq("GET", serverURL()+"/api/latest", "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach server\nstart with: toss serve\n")
		os.Exit(1)
	}
	defer resp.Body.Close()
	checkResp(resp)
	if resp.StatusCode == 404 {
		fmt.Fprintln(os.Stderr, "no items yet")
		os.Exit(1)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		fmt.Fprintln(os.Stderr, "latest is a file — use toss get")
		os.Exit(1)
	}
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if err := clipWrite(text); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	preview := text
	if len(preview) > 60 {
		preview = preview[:60] + "..."
	}
	fmt.Fprintf(os.Stderr, "copied: %s\n", preview)
}

// --- watch ---

func formatSize(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%d B", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	}
}

func sendNotification(title, body string) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, body, title)
		exec.Command("osascript", "-e", script).Run()
	case "linux":
		exec.Command("notify-send", title, body).Run()
	}
}

func watch(withNotify bool) {
	url := serverURL()
	fmt.Fprintf(os.Stderr, "watching %s\n", url)

	lastID := 0
	if resp, err := doReq("GET", url+"/api/items", "", nil); err == nil {
		checkResp(resp)
		var items []item
		json.NewDecoder(resp.Body).Decode(&items)
		resp.Body.Close()
		for _, it := range items {
			if it.ID > lastID {
				lastID = it.ID
			}
		}
	}

	for {
		time.Sleep(time.Second)
		resp, err := doReq("GET", url+"/api/items", "", nil)
		if err != nil {
			continue
		}
		checkResp(resp)
		var items []item
		json.NewDecoder(resp.Body).Decode(&items)
		resp.Body.Close()

		for _, it := range items {
			if it.ID <= lastID {
				continue
			}
			lastID = it.ID
			switch it.Type {
			case "text":
				fmt.Println(it.Content)
				if withNotify {
					sendNotification("toss", it.Content)
				}
			case "file":
				fmt.Fprintf(os.Stderr, "file: %s (%s)\n", it.Filename, formatSize(it.Size))
				if withNotify {
					sendNotification("toss", fmt.Sprintf("file: %s (%s)", it.Filename, formatSize(it.Size)))
				}
			}
		}
	}
}

// --- client ---

func serverURL() string {
	host := os.Getenv("TOSS_HOST")
	if host == "" {
		if found := discover(); found != "" {
			host = found
		} else {
			fmt.Fprintf(os.Stderr, "no server found — starting one in background\n")
			if found := autoServe(); found != "" {
				host = found
			} else {
				fmt.Fprintf(os.Stderr, "error: could not start server\nstart manually with: toss serve\n")
				os.Exit(1)
			}
		}
	}
	if !strings.HasPrefix(host, "http") {
		host = "http://" + host
	}
	return strings.TrimRight(host, "/")
}

func sendText(text string) {
	resp, err := doReq("POST", serverURL()+"/api/text", "text/plain", strings.NewReader(text))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach server\nstart with: toss serve\n")
		os.Exit(1)
	}
	defer resp.Body.Close()
	checkResp(resp)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: %s\n", b)
		os.Exit(1)
	}
	fmt.Println("sent")
}

func sendFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	io.Copy(part, file)
	w.Close()

	resp, err := doReq("POST", serverURL()+"/api/file", w.FormDataContentType(), &buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach server\nstart with: toss serve\n")
		os.Exit(1)
	}
	defer resp.Body.Close()
	checkResp(resp)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: %s\n", b)
		os.Exit(1)
	}
	fmt.Printf("sent: %s\n", filepath.Base(path))
}

func getLatest() {
	resp, err := doReq("GET", serverURL()+"/api/latest", "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach server\nstart with: toss serve\n")
		os.Exit(1)
	}
	defer resp.Body.Close()
	checkResp(resp)
	if resp.StatusCode == 404 {
		fmt.Fprintln(os.Stderr, "no items yet")
		os.Exit(1)
	}

	cd := resp.Header.Get("Content-Disposition")
	if cd != "" {
		_, params, _ := mime.ParseMediaType(cd)
		name := params["filename"]
		if name == "" {
			name = "download"
		}
		f, err := os.Create(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		n, _ := io.Copy(f, resp.Body)
		fmt.Fprintf(os.Stderr, "saved: %s (%d bytes)\n", name, n)
	} else {
		io.Copy(os.Stdout, resp.Body)
		if stat, err := os.Stdout.Stat(); err == nil && (stat.Mode()&os.ModeCharDevice) != 0 {
			fmt.Println()
		}
	}
}

// --- chat ---

func chat(withNotify bool) {
	url := serverURL()
	fmt.Fprintf(os.Stderr, "connected to %s\ntype a message and press enter. ctrl+c to exit.\n\n", url)

	var mu sync.Mutex
	lastID := 0

	if resp, err := doReq("GET", url+"/api/items", "", nil); err == nil {
		checkResp(resp)
		var items []item
		json.NewDecoder(resp.Body).Decode(&items)
		resp.Body.Close()
		for _, it := range items {
			if it.ID > lastID {
				lastID = it.ID
			}
		}
	}

	go func() {
		for {
			time.Sleep(time.Second)
			resp, err := doReq("GET", url+"/api/items", "", nil)
			if err != nil {
				continue
			}
			var items []item
			json.NewDecoder(resp.Body).Decode(&items)
			resp.Body.Close()

			mu.Lock()
			for _, it := range items {
				if it.ID <= lastID {
					continue
				}
				lastID = it.ID
				switch it.Type {
				case "text":
					fmt.Printf("\r← %s\n> ", it.Content)
					if withNotify {
						sendNotification("toss", it.Content)
					}
				case "file":
					fmt.Printf("\r← file: %s (%s)\n> ", it.Filename, formatSize(it.Size))
					if withNotify {
						sendNotification("toss", fmt.Sprintf("file: %s (%s)", it.Filename, formatSize(it.Size)))
					}
				}
			}
			mu.Unlock()
		}
	}()

	fmt.Print("> ")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			fmt.Print("> ")
			continue
		}
		resp, err := doReq("POST", url+"/api/text", "text/plain", strings.NewReader(text))
		if err != nil {
			fmt.Fprintf(os.Stderr, "\rerror: %v\n> ", err)
			continue
		}
		var sent item
		json.NewDecoder(resp.Body).Decode(&sent)
		resp.Body.Close()
		mu.Lock()
		if sent.ID > lastID {
			lastID = sent.ID
		}
		mu.Unlock()
		fmt.Print("> ")
	}
}

// --- update ---

func extractTarGz(archivePath, destDir, target string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == target {
			out, err := os.OpenFile(filepath.Join(destDir, target), os.O_CREATE|os.O_WRONLY, 0755)
			if err != nil {
				return err
			}
			io.Copy(out, tr)
			out.Close()
			return nil
		}
	}
	return fmt.Errorf("binary not found in archive")
}

func extractZip(archivePath, destDir, target string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if filepath.Base(f.Name) == target {
			src, err := f.Open()
			if err != nil {
				return err
			}
			out, err := os.OpenFile(filepath.Join(destDir, target), os.O_CREATE|os.O_WRONLY, 0755)
			if err != nil {
				src.Close()
				return err
			}
			io.Copy(out, src)
			src.Close()
			out.Close()
			return nil
		}
	}
	return fmt.Errorf("binary not found in archive")
}

func replaceBinary(newPath, exePath string) error {
	if err := os.Rename(newPath, exePath); err == nil {
		return nil
	}
	// cross-device: try copy (remove first to avoid "text file busy")
	if data, err := os.ReadFile(newPath); err == nil {
		os.Remove(exePath)
		if err := os.WriteFile(exePath, data, 0755); err == nil {
			return nil
		}
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("run as administrator to update")
	}
	fmt.Println("need sudo to update")
	cmd := exec.Command("sudo", "cp", newPath, exePath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return exec.Command("sudo", "chmod", "+x", exePath).Run()
}

func update() {
	fmt.Printf("current: %s\n", version)

	resp, err := http.Get("https://api.github.com/repos/sulemaanhamza/toss/releases/latest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	latest := release.TagName
	if latest == "" {
		fmt.Fprintln(os.Stderr, "error: could not find latest release")
		os.Exit(1)
	}
	if latest == version {
		fmt.Println("already up to date")
		return
	}
	fmt.Printf("found %s\n", latest)

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	ver := strings.TrimPrefix(latest, "v")
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	archive := fmt.Sprintf("toss_%s_%s_%s.%s", ver, goos, goarch, ext)
	url := fmt.Sprintf("https://github.com/sulemaanhamza/toss/releases/download/%s/%s", latest, archive)

	fmt.Printf("downloading %s\n", archive)
	dlResp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "error: download failed (HTTP %d)\n", dlResp.StatusCode)
		os.Exit(1)
	}

	tmpDir, err := os.MkdirTemp("", "toss-update-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, archive)
	f, err := os.Create(archivePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	io.Copy(f, dlResp.Body)
	f.Close()

	binaryName := "toss"
	if goos == "windows" {
		binaryName = "toss.exe"
	}

	if ext == "tar.gz" {
		err = extractTarGz(archivePath, tmpDir, binaryName)
	} else {
		err = extractZip(archivePath, tmpDir, binaryName)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	if err := replaceBinary(filepath.Join(tmpDir, binaryName), exe); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("updated to %s\n", latest)
}

func uninstall() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot find toss binary: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("this will remove:\n")
	fmt.Printf("  binary:  %s\n", exe)
	cfgDir := configDir()
	hasCfg := false
	if _, err := os.Stat(cfgDir); err == nil {
		hasCfg = true
		fmt.Printf("  config:  %s\n", cfgDir)
	}
	svcPath := servicePath()
	hasSvc := false
	if svcPath != "" {
		if _, err := os.Stat(svcPath); err == nil {
			hasSvc = true
			fmt.Printf("  service: %s\n", svcPath)
		}
	}
	fmt.Print("\nuninstall? [y/N] ")
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" && answer != "yes" {
		fmt.Println("cancelled")
		return
	}

	if hasSvc {
		switch runtime.GOOS {
		case "darwin":
			exec.Command("launchctl", "unload", svcPath).Run()
		case "linux":
			exec.Command("systemctl", "--user", "disable", "--now", "toss").Run()
		}
		os.Remove(svcPath)
		if runtime.GOOS == "linux" {
			exec.Command("systemctl", "--user", "daemon-reload").Run()
		}
		fmt.Printf("removed %s\n", svcPath)
	}

	if hasCfg {
		os.RemoveAll(cfgDir)
		fmt.Printf("removed %s\n", cfgDir)
	}

	if err := os.Remove(exe); err != nil {
		if os.IsPermission(err) {
			fmt.Println("need sudo to remove binary")
			cmd := exec.Command("sudo", "rm", exe)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Printf("removed %s\n", exe)
	fmt.Println("toss uninstalled")
}

func printUsage() {
	fmt.Print(`toss - share text and files on your local network

usage:
  toss "hello world"      send text
  toss ./file.png         send a file
  echo "hi" | toss        pipe text
  toss get                get latest item
  toss paste              send clipboard contents
  toss copy               copy latest to clipboard
  toss watch              stream new items to terminal
  toss watch --notify     also show desktop notifications
  toss chat               interactive two-way chat
  toss chat --notify      also show desktop notifications
  toss config             set shared key for auth
  toss serve              start server (auto-starts if needed)
  toss serve --install    run server on login (launchd/systemd)
  toss serve --uninstall  remove login service
  toss update             update to latest version
  toss uninstall          remove toss from your system

server auto-starts in the background when needed.
override with: export TOSS_HOST=<ip>:9090
`)
}

func main() {
	if len(os.Args) < 2 {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			body, _ := io.ReadAll(os.Stdin)
			text := strings.TrimSpace(string(body))
			if text != "" {
				sendText(text)
				return
			}
		}
		printUsage()
		return
	}

	switch os.Args[1] {
	case "serve":
		if len(os.Args) > 2 {
			switch os.Args[2] {
			case "--install":
				serviceInstall()
			case "--uninstall":
				serviceUninstall()
			default:
				fmt.Fprintf(os.Stderr, "unknown flag: %s\n", os.Args[2])
				os.Exit(1)
			}
		} else {
			serve()
		}
	case "get":
		getLatest()
	case "paste":
		paste()
	case "copy":
		copyLatest()
	case "watch":
		watch(len(os.Args) > 2 && os.Args[2] == "--notify")
	case "chat":
		chat(len(os.Args) > 2 && os.Args[2] == "--notify")
	case "config":
		configure()
	case "update":
		update()
	case "uninstall":
		uninstall()
	case "-v", "--version", "version":
		fmt.Println(version)
	case "-h", "--help", "help":
		printUsage()
	default:
		arg := os.Args[1]
		if info, err := os.Stat(arg); err == nil && !info.IsDir() {
			sendFile(arg)
		} else {
			sendText(strings.Join(os.Args[1:], " "))
		}
	}
}
