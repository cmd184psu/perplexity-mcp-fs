package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var allowedRoots []string

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

func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	for _, root := range allowedRoots {
		if strings.HasPrefix(abs, root+string(filepath.Separator)) || abs == root {
			return abs, nil
		}
	}
	return "", fmt.Errorf("path %q is outside allowed roots", abs)
}

func buildServer(logger *log.Logger) *server.MCPServer {
	s := server.NewMCPServer("mcp-fs", "1.1.0", server.WithToolCapabilities(false))

	s.AddTool(
		mcp.NewTool("read_file",
			mcp.WithDescription("Read the complete contents of a file"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path to the file")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			raw := req.GetString("path", "")
			abs, err := resolvePath(raw)
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
			raw := req.GetString("path", "")
			content := req.GetString("content", "")
			abs, err := resolvePath(raw)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
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
			raw := req.GetString("path", "")
			oldStr := req.GetString("old_str", "")
			newStr := req.GetString("new_str", "")
			abs, err := resolvePath(raw)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("read error: %v", err)), nil
			}
			src := string(data)
			if !strings.Contains(src, oldStr) {
				return mcp.NewToolResultError("old_str not found in file"), nil
			}
			updated := strings.Replace(src, oldStr, newStr, 1)
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
			raw := req.GetString("path", "")
			abs, err := resolvePath(raw)
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
			raw := req.GetString("path", "")
			pattern := req.GetString("pattern", "*")
			abs, err := resolvePath(raw)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			var matches []string
			_ = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() && skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				if !d.IsDir() {
					ok, _ := filepath.Match(pattern, d.Name())
					if ok {
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
			raw := req.GetString("path", "")
			abs, err := resolvePath(raw)
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
			raw := req.GetString("path", "")
			abs, err := resolvePath(raw)
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
			raw := req.GetString("path", "")
			abs, err := resolvePath(raw)
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
			result := fmt.Sprintf(
				"path: %s\ntype: %s\nsize: %d bytes\nmod_time: %s\npermissions: %s\n",
				abs, kind, info.Size(), info.ModTime().Format("2006-01-02 15:04:05"), info.Mode().String(),
			)
			return mcp.NewToolResultText(result), nil
		},
	)

	return s
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: mcp-fs [-log <file>] [-sse] [-port <n>] <root1> [root2 ...]\n\nFlags:\n")
		flag.PrintDefaults()
	}
	logPath := flag.String("log", "", "path to log file (default: stderr)")
	sseMode := flag.Bool("sse", false, "serve over HTTP/SSE instead of stdio")
	port    := flag.Int("port", 8765, "port to listen on in SSE mode")
	flag.Parse()

	roots := flag.Args()
	if len(roots) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	var logOut io.Writer = os.Stderr
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcp-fs: cannot open log file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		logOut = f
	}
	logger := log.New(logOut, "[mcp-fs] ", log.LstdFlags)

	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			logger.Fatalf("invalid root %q: %v", r, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			logger.Fatalf("root %q does not exist: %v", abs, err)
		}
		if !info.IsDir() {
			logger.Fatalf("root %q is not a directory", abs)
		}
		allowedRoots = append(allowedRoots, abs)
		logger.Printf("allowed root: %s", abs)
	}

	s := buildServer(logger)

	if *sseMode {
		addr := fmt.Sprintf(":%d", *port)
		baseURL := fmt.Sprintf("http://localhost:%d", *port)
		logger.Printf("mcp-fs ready in SSE mode on %s", baseURL)
		sse := server.NewSSEServer(s, server.WithBaseURL(baseURL))
		if err := sse.Start(addr); err != nil {
			logger.Fatalf("SSE server error: %v", err)
		}
	} else {
		logger.Printf("mcp-fs ready, serving %d root(s) via stdio", len(allowedRoots))
		if err := server.ServeStdio(s); err != nil && !errors.Is(err, context.Canceled) {
			logger.Fatalf("server error: %v", err)
		}
		logger.Printf("session complete")
	}
}

