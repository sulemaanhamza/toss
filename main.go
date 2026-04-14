package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML string

const (
	defaultPort = "9090"
	maxItems    = 64
	maxFileSize = 100 << 20
)

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

// --- HTTP handlers ---

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("GET /api/items", handleItems)
	mux.HandleFunc("POST /api/text", handleText)
	mux.HandleFunc("POST /api/file", handleFile)
	mux.HandleFunc("GET /api/download/{id}", handleDownload)
	mux.HandleFunc("GET /api/latest", handleLatest)

	fmt.Printf("toss running on :%s\n\n", port)
	for _, ip := range localIPs() {
		fmt.Printf("  http://%s:%s\n", ip, port)
	}
	fmt.Printf("\non other machines:\n  export TOSS_HOST=<ip>:%s\n\n", port)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// --- client ---

func serverURL() string {
	host := os.Getenv("TOSS_HOST")
	if host == "" {
		host = "localhost:" + defaultPort
	}
	if !strings.HasPrefix(host, "http") {
		host = "http://" + host
	}
	return strings.TrimRight(host, "/")
}

func sendText(text string) {
	resp, err := http.Post(serverURL()+"/api/text", "text/plain", strings.NewReader(text))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach server at %s\nstart with: toss serve\n", serverURL())
		os.Exit(1)
	}
	defer resp.Body.Close()
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

	resp, err := http.Post(serverURL()+"/api/file", w.FormDataContentType(), &buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach server at %s\nstart with: toss serve\n", serverURL())
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: %s\n", b)
		os.Exit(1)
	}
	fmt.Printf("sent: %s\n", filepath.Base(path))
}

func getLatest() {
	resp, err := http.Get(serverURL() + "/api/latest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach server at %s\nstart with: toss serve\n", serverURL())
		os.Exit(1)
	}
	defer resp.Body.Close()
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

env:
  TOSS_HOST  server address (default: localhost:9090)
  TOSS_PORT  server port (default: 9090)
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
