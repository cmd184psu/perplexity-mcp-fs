package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registeredTools holds every mcp.Tool added to the MCP server so that
// the /api/tools endpoint and tests can inspect them without relying on
// unexported fields of *server.MCPServer (which mcp-go v0.32.0 does not expose).

// ── Tool handler functions ─────────────────────────────────────────────────────

func handleReadFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	acquireWorker()
	defer releaseWorker()

	abs, err := resolvePath(req.GetString("path", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result, err := withConsistentReadCtx(ctx, abs, func() (string, error) {
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("read error: %v", err)
		}
		logger.Printf("read_file: %s (%d bytes)", abs, len(data))
		return string(data), nil
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(result), nil
}

func handleReadFiles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawPaths := req.GetArguments()["paths"]
	paths, ok := rawPaths.([]any)
	if !ok || len(paths) == 0 {
		return mcp.NewToolResultError("paths must be a non-empty array"), nil
	}

	var sb strings.Builder
	for i, raw := range paths {
		path, ok := raw.(string)
		if !ok || strings.TrimSpace(path) == "" {
			return mcp.NewToolResultError("paths entries must be non-empty strings"), nil
		}

		abs, err := resolvePath(path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		acquireWorker()
		defer releaseWorker()

		result, err := withConsistentReadCtx(ctx, abs, func() (string, error) {
			data, err := os.ReadFile(abs)
			if err != nil {
				return "", fmt.Errorf("read error for %s: %v", abs, err)
			}
			return string(data), nil
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("==> ")
		sb.WriteString(abs)
		sb.WriteString(" <==\n")
		sb.WriteString(result)
	}

	logger.Printf("read_files: %d files", len(paths))
	return mcp.NewToolResultText(sb.String()), nil
}

func handleWriteFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	acquireWorker()
	defer releaseWorker()

	abs, err := resolvePath(req.GetString("path", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	content := req.GetString("content", "")
	result, err := withQueuedWriteCtx(ctx, abs, func() (string, error) {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return "", fmt.Errorf("mkdir error: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			return "", fmt.Errorf("write error: %v", err)
		}
		logger.Printf("write_file: %s (%d bytes)", abs, len(content))
		return fmt.Sprintf("wrote %d bytes to %s", len(content), abs), nil
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(result), nil
}

func handlePatchFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	acquireWorker()
	defer releaseWorker()

	abs, err := resolvePath(req.GetString("path", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	oldStr := req.GetString("old_str", "")
	newStr := req.GetString("new_str", "")
	result, err := withQueuedWriteCtx(ctx, abs, func() (string, error) {
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("read error: %v", err)
		}

		src := string(data)
		if !strings.Contains(src, oldStr) {
			return "", fmt.Errorf("old_str not found in file")
		}

		updated := strings.Replace(src, oldStr, newStr, 1)
		if err := os.WriteFile(abs, []byte(updated), 0644); err != nil {
			return "", fmt.Errorf("write error: %v", err)
		}

		logger.Printf("patch_file: %s", abs)
		return fmt.Sprintf("patched %s", abs), nil
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(result), nil
}

func handleListDirectory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
}

func handleSearchFiles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
}

func handleCreateDirectory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	abs, err := resolvePath(req.GetString("path", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := os.MkdirAll(abs, 0755); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("mkdir error: %v", err)), nil
	}

	logger.Printf("create_directory: %s", abs)
	return mcp.NewToolResultText(fmt.Sprintf("created %s", abs)), nil
}

func handleDeleteFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
}

func handleGetFileInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
}

func handleMakeBuild(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out, err := runMakeTarget(req.GetString("project_root", ""), "build")
	if err != nil {
		return mcp.NewToolResultError(out), nil
	}
	logger.Printf("make_build: %s", req.GetString("project_root", ""))
	return mcp.NewToolResultText(out), nil
}

func handleMakeTest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out, err := runMakeTarget(req.GetString("project_root", ""), "test")
	if err != nil {
		return mcp.NewToolResultError(out), nil
	}
	logger.Printf("make_test: %s", req.GetString("project_root", ""))
	return mcp.NewToolResultText(out), nil
}

func handleUpdateSessionMd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	msg, err := appendSessionNote(
		req.GetString("project_root", ""),
		req.GetString("summary", ""),
		req.GetString("next_steps", ""),
	)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	logger.Printf("update_session_md: %s", req.GetString("project_root", ""))
	return mcp.NewToolResultText(msg), nil
}

// ── buildMCPServer ─────────────────────────────────────────────────────────────

func buildMCPServer() *server.MCPServer {
	registeredTools = nil

	s := server.NewMCPServer("mcp-fs", "1.3.0", server.WithToolCapabilities(false))
	add := func(t mcp.Tool, h server.ToolHandlerFunc) {
		registeredTools = append(registeredTools, t)
		s.AddTool(t, h)
	}

	add(
		mcp.NewTool("read_file",
			mcp.WithDescription("Read the complete contents of a file"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path to the file")),
		),
		handleReadFile,
	)

	add(
		mcp.NewTool("read_files",
			mcp.WithDescription("Read the complete contents of multiple files"),
			mcp.WithArray("paths", mcp.Required(), mcp.Description("Array of absolute or relative file paths to read")),
		),
		handleReadFiles,
	)

	add(
		mcp.NewTool("write_file",
			mcp.WithDescription("Write (or overwrite) a file with the given content"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path to the file")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Text content to write")),
		),
		handleWriteFile,
	)

	add(
		mcp.NewTool("patch_file",
			mcp.WithDescription("Replace the first occurrence of old_str with new_str in a file"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file")),
			mcp.WithString("old_str", mcp.Required(), mcp.Description("Exact string to find")),
			mcp.WithString("new_str", mcp.Required(), mcp.Description("Replacement string")),
		),
		handlePatchFile,
	)

	add(
		mcp.NewTool("list_directory",
			mcp.WithDescription("List files and directories at the given path (non-recursive)"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Directory path to list")),
		),
		handleListDirectory,
	)

	add(
		mcp.NewTool("search_files",
			mcp.WithDescription("Recursively find files whose names match a glob pattern under a directory"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Root directory to search")),
			mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern, e.g. '*.go' or 'main*'")),
		),
		handleSearchFiles,
	)

	add(
		mcp.NewTool("create_directory",
			mcp.WithDescription("Create a directory (and any necessary parents)"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Directory path to create")),
		),
		handleCreateDirectory,
	)

	add(
		mcp.NewTool("delete_file",
			mcp.WithDescription("Delete a file (not a directory)"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file to delete")),
		),
		handleDeleteFile,
	)

	add(
		mcp.NewTool("get_file_info",
			mcp.WithDescription("Return metadata about a file or directory (size, mod time, permissions)"),
			mcp.WithString("path", mcp.Required(), mcp.Description("Path to inspect")),
		),
		handleGetFileInfo,
	)

	add(
		mcp.NewTool("make_build",
			mcp.WithDescription("Run 'make build' from the top of the specified project root. The root must contain session.md."),
			mcp.WithString("project_root", mcp.Required(), mcp.Description("Project root directory; must be an allowed root and contain session.md")),
		),
		handleMakeBuild,
	)

	add(
		mcp.NewTool("make_test",
			mcp.WithDescription("Run 'make test' from the top of the specified project root. The root must contain session.md."),
			mcp.WithString("project_root", mcp.Required(), mcp.Description("Project root directory; must be an allowed root and contain session.md")),
		),
		handleMakeTest,
	)

	add(
		mcp.NewTool("update_session_md",
			mcp.WithDescription("Append a timestamped note to session.md at the top of the specified project root"),
			mcp.WithString("project_root", mcp.Required(), mcp.Description("Project root directory; must be an allowed root and contain session.md")),
			mcp.WithString("summary", mcp.Required(), mcp.Description("Short summary of what changed or was learned")),
			mcp.WithString("next_steps", mcp.Description("Optional next steps for the project")),
		),
		handleUpdateSessionMd,
	)

	return s
}
