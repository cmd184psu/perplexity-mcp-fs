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
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

//go:embed static
var staticFiles embed.FS

// ── Config ─────────────────────────────────────────────────────────────────────

type Config struct {
	Port  int      `json:"port"`
	Roots []string `json:"roots"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mcp-fs-sse.json")
}

func loadConfig() Config {
	cfg := Config{Port: 8765}
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	return cfg
}

func saveConfig(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0644)
}

// ── Globals ───────────────────────────────────────────────────────────────────

var (
	allowedRoots []string
	rootsMu      sync.RWMutex
	logger       = log.New(io.Discard, "", 0)
)

// SkipDirs are directory names ignored by list_directory and search_files.
var skipDirs = map[string]bool{
	".git":          true,
	".svn":          true,
	".hg":           true,
	"node_modules":  true,
	".node_modules": true,
	"vendor":        true,
	".venv":         true,
	"venv":          true,
	"__pycache__":   true,
	".pytest_cache": true,
	".mypy_cache":   true,
	"dist":          true,
	"build":         true,
	".next":         true,
	".nuxt":         true,
	".turbo":        true,
	".cache":        true,
	".DS_Store":     true,
}

// ── Path resolution ──────────────────────────────────────────────────────────────

func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	rootsMu.RLock()
	defer rootsMu.RUnlock()
	for _, root := range allowedRoots {
		if strings.HasPrefix(abs, root+string(filepath.Separator)) || abs == root {
			return abs, nil
		}
	}
	return "", fmt.Errorf("path %q is outside allowed roots", abs)
}

func setRoots(roots []string) error {
	var validated []string
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			return fmt.Errorf("invalid root %q: %w", r, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("root %q does not exist: %w", abs, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("root %q is not a directory", abs)
		}
		validated = append(validated, abs)
	}
	rootsMu.Lock()
	allowedRoots = validated
	rootsMu.Unlock()
	logger.Printf("roots updated: %v", validated)
	return nil
}

// ── MCP tools ───────────────────────────────────────────────────────────────────

func buildMCPServer() *server.MCPServer {
	s := server.NewMCPServer("mcp-fs", "1.3.0", server.WithToolCapabilities(false))

	s.AddTool(
		mcp.NewTool("read_file",
			mcp.WithDescription("Read the complete contents of a file"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path to the file")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			abs, err := resolvePath(req.GetString("path", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("read error: %v", err)), nil
			}
			logger.Printf("read_file: %s (%d bytes)", abs, len(data))
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("write_file",
			mcp.WithDescription("Write (or overwrite) a file with the given content"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path to the file")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Text content to write")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			abs, err := resolvePath(req.GetString("path", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			content := req.GetString("content", "")
			if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("mkdir error: %v", err)), nil
			}
			if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("write error: %v", err)), nil
			}
			logger.Printf("write_file: %s (%d bytes)", abs, len(content))
			return mcp.NewToolResultText(fmt.Sprintf("wrote %d bytes to %s", len(content), abs)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("patch_file",
			mcp.WithDescription("Replace the first occurrence of old_str with new_str in a file"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file")),
			mcp.WithString("old_str", mcp.Required(), mcp.Description("Exact string to find")),
			mcp.WithString("new_str", mcp.Required(), mcp.Description("Replacement string")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			abs, err := resolvePath(req.GetString("path", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("read error: %v", err)), nil
			}
			src := string(data)
			oldStr := req.GetString("old_str", "")
			if !strings.Contains(src, oldStr) {
				return mcp.NewToolResultError("old_str not found in file"), nil
			}
			updated := strings.Replace(src, oldStr, req.GetString("new_str", ""), 1)
			if err := os.WriteFile(abs, []byte(updated), 0644); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("write error: %v", err)), nil
			}
			logger.Printf("patch_file: %s", abs)
			return mcp.NewToolResultText(fmt.Sprintf("patched %s", abs)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("list_directory",
			mcp.WithDescription("List files and directories at the given path (non-recursive)"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Directory path to list")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			abs, err := resolvePath(req.GetString("path", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			entries, err := os.ReadDir(abs)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("readdir error: %v", err)), nil
			}
			var sb strings.Builder
			for _, e := range entries {
				if e.IsDir() && skipDirs[e.Name()] {
					continue
				}
				if e.IsDir() {
					sb.WriteString("[DIR]  ")
				} else {
					sb.WriteString("[FILE] ")
				}
				sb.WriteString(e.Name())
				sb.WriteByte('\n')
			}
			logger.Printf("list_directory: %s (%d entries)", abs, len(entries))
			return mcp.NewToolResultText(sb.String()), nil
		},
	)

	s.AddTool(
		mcp.NewTool("search_files",
			mcp.WithDescription("Recursively find files whose names match a glob pattern under a directory"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Root directory to search")),
			mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern, e.g. '*.go' or 'main*'")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			abs, err := resolvePath(req.GetString("path", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			pattern := req.GetString("pattern", "*")
			var matches []string
			_ = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() && skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				if !d.IsDir() {
					if ok, _ := filepath.Match(pattern, d.Name()); ok {
						matches = append(matches, p)
					}
				}
				return nil
			})
			logger.Printf("search_files: %s pattern=%s (%d results)", abs, pattern, len(matches))
			return mcp.NewToolResultText(strings.Join(matches, "\n")), nil
		},
	)

	s.AddTool(
		mcp.NewTool("create_directory",
			mcp.WithDescription("Create a directory (and any necessary parents)"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Directory path to create")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			abs, err := resolvePath(req.GetString("path", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := os.MkdirAll(abs, 0755); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("mkdir error: %v", err)), nil
			}
			logger.Printf("create_directory: %s", abs)
			return mcp.NewToolResultText(fmt.Sprintf("created %s", abs)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("delete_file",
			mcp.WithDescription("Delete a file (not a directory)"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file to delete")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			abs, err := resolvePath(req.GetString("path", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			info, err := os.Stat(abs)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("stat error: %v", err)), nil
			}
			if info.IsDir() {
				return mcp.NewToolResultError("path is a directory; use shell tools to remove directories"), nil
			}
			if err := os.Remove(abs); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("delete error: %v", err)), nil
			}
			logger.Printf("delete_file: %s", abs)
			return mcp.NewToolResultText(fmt.Sprintf("deleted %s", abs)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("get_file_info",
			mcp.WithDescription("Return metadata about a file or directory (size, mod time, permissions)"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Path to inspect")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			abs, err := resolvePath(req.GetString("path", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			info, err := os.Stat(abs)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("stat error: %v", err)), nil
			}
			kind := "file"
			if info.IsDir() {
				kind = "directory"
			}
			return mcp.NewToolResultText(fmt.Sprintf(
				"path: %s\ntype: %s\nsize: %d bytes\nmod_time: %s\npermissions: %s\n",
				abs, kind, info.Size(), info.ModTime().Format("2006-01-02 15:04:05"), info.Mode().String(),
			)), nil
		},
	)

	return s
}

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
	port    := flag.Int("port", 0, "port for SSE mode (overrides config, default 8765)")
	flag.Parse()

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
	if *port != 0 {
		cfg.Port = *port
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
	mux.Handle("/message", sseSrv)

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

	addr := fmt.Sprintf(":%d", cfg.Port)
	logger.Printf("mcp-fs SSE listening on http://localhost%s", addr)
	logger.Printf("  GUI:          http://localhost%s/", addr)
	logger.Printf("  SSE endpoint: http://localhost%s/sse", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Fatalf("http server error: %v", err)
	}
}
