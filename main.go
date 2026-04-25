package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

//go:embed static
var staticFiles embed.FS

// ── Config ─────────────────────────────────────────────────────────────────────

// ── Globals ───────────────────────────────────────────────────────────────────

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization, Mcp-Session-Id, Last-Event-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Path resolution ──────────────────────────────────────────────────────────────

// ── MCP tools ───────────────────────────────────────────────────────────────────

// registeredTools holds every mcp.Tool added to the MCP server so that
// the /api/tools endpoint and tests can inspect them without relying on
// unexported fields of *server.MCPServer (which mcp-go v0.32.0 does not expose).
var registeredTools []mcp.Tool

// ── API handlers ──────────────────────────────────────────────────────────────────

type browseEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

func handleBrowse(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var result []browseEntry
	for _, e := range entries {
		if skipDirs[e.Name()] {
			continue
		}
		if e.IsDir() {
			result = append(result, browseEntry{
				Name:  e.Name(),
				Path:  filepath.Join(path, e.Name()),
				IsDir: true,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func handleGetRoots(w http.ResponseWriter, r *http.Request) {
	rootsMu.RLock()
	roots := append([]string{}, allowedRoots...)
	rootsMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roots)
}

func handleSetRoots(w http.ResponseWriter, r *http.Request) {
	var roots []string
	if err := json.NewDecoder(r.Body).Decode(&roots); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := setRoots(roots); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := loadConfig()
	cfg.Roots = roots
	if err := saveConfig(cfg); err != nil {
		http.Error(w, "saved roots but failed to write config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "roots": roots})
}

// ── main ──────────────────────────────────────────────────────────────────────────

func main() {
	logPath := flag.String("log", "", "path to log file (default: stderr)")
	sseMode := flag.Bool("sse", false, "serve over HTTP/SSE instead of stdio")
	var port int
	flag.IntVar(&port, "port", 8765, "port for SSE mode (overrides config, default 8765)")
	flag.IntVar(&maxWorkers, "workers", maxWorkers, "maximum concurrent file operation workers (default 20)")
	flag.Parse()
	workerSem = make(chan struct{}, maxWorkers)

	// Logger
	var logOut io.Writer = os.Stderr
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot open log file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		logOut = f
	}
	logger = log.New(logOut, "", log.LstdFlags)

	// Load config
	cfg := loadConfig()
	if port != 8765 {
		cfg.Port = port
	}

	// Roots: CLI args override config
	initialRoots := cfg.Roots
	if flag.NArg() > 0 {
		initialRoots = flag.Args()
	}
	if len(initialRoots) > 0 {
		if err := setRoots(initialRoots); err != nil {
			logger.Fatalf("invalid root: %v", err)
		}
	}

	mcpSrv := buildMCPServer()

	if !*sseMode {
		// Stdio mode
		stdioSrv := server.NewStdioServer(mcpSrv)
		if err := stdioSrv.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
			logger.Fatalf("stdio server error: %v", err)
		}
		return
	}

	// SSE mode — static files are embedded in the binary via //go:embed
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		logger.Fatalf("embed sub error: %v", err)
	}
	logger.Printf("serving embedded static files")

	mux := http.NewServeMux()

	// MCP SSE endpoint
	sseSrv := server.NewSSEServer(mcpSrv, server.WithBaseURL(fmt.Sprintf("http://localhost:%d", cfg.Port)))
	mux.Handle("/sse", sseSrv)
	// /message handles MCP JSON-RPC: tools/list, tools/call, etc.
	mux.Handle("/message", withCORS(sseSrv))

	// API
	mux.HandleFunc("/api/browse", handleBrowse)
	mux.HandleFunc("/api/roots", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGetRoots(w, r)
		case http.MethodPost:
			handleSetRoots(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	// Expose registered MCP tools for discovery
	mux.HandleFunc("/api/tools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(registeredTools)
	})

	// Static UI — served from embedded FS
	fileServer := http.FileServer(http.FS(staticFS))
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

	// Root → index.html
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			f, err := staticFS.Open("index.html")
			if err != nil {
				http.Error(w, "index.html not found", http.StatusInternalServerError)
				return
			}
			f.Close()
			http.ServeFileFS(w, r, staticFS, "index.html")
			return
		}
		http.NotFound(w, r)
	})

	addr := fmt.Sprintf(":%d", port)
	logger.Printf("mcp-fs SSE listening on http://localhost%s", addr)
	logger.Printf("  GUI:          http://localhost%s/", addr)
	logger.Printf("  SSE endpoint: http://localhost%s/sse", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Fatalf("http server error: %v", err)
	}
}
