package main

import (
	"bufio"
	"bytes"
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

func watch() {
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
			case "file":
				fmt.Fprintf(os.Stderr, "file: %s (%s)\n", it.Filename, formatSize(it.Size))
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
			host = "localhost:" + defaultPort
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

func printUsage() {
	fmt.Print(`toss - share text and files on your local network

usage:
  toss serve              start the server
  toss "hello world"      send text
  toss ./file.png         send a file
  echo "hi" | toss        pipe text
  toss get                get latest item
  toss paste              send clipboard contents
  toss copy               copy latest to clipboard
  toss watch              watch for new items
  toss config             set shared key for auth

server is auto-discovered on the local network.
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
		serve()
	case "get":
		getLatest()
	case "paste":
		paste()
	case "copy":
		copyLatest()
	case "watch":
		watch()
	case "config":
		configure()
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
