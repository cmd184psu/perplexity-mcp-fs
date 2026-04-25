package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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

func resolveProjectRoot(projectRoot string) (string, error) {
	abs, err := resolvePath(projectRoot)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("project root stat error: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project root %q is not a directory", abs)
	}
	if _, err := os.Stat(filepath.Join(abs, "session.md")); err != nil {
		return "", fmt.Errorf("project root %q must contain session.md", abs)
	}
	return abs, nil
}

func runMakeTarget(projectRoot, target string) (string, error) {
	root, err := resolveProjectRoot(projectRoot)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "make", target)
	cmd.Dir = root

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	duration := time.Since(start)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("cwd: %s\n", root))
	sb.WriteString(fmt.Sprintf("command: make %s\n", target))
	sb.WriteString(fmt.Sprintf("duration: %s\n", duration.Round(time.Millisecond)))
	if err != nil {
		sb.WriteString(fmt.Sprintf("exit: error (%v)\n", err))
	} else {
		sb.WriteString("exit: 0\n")
	}
	if stdout.Len() > 0 {
		sb.WriteString("\nstdout:\n")
		sb.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		sb.WriteString("\nstderr:\n")
		sb.WriteString(stderr.String())
	}

	if ctx.Err() == context.DeadlineExceeded {
		return sb.String(), fmt.Errorf("command timed out")
	}
	if err != nil {
		return sb.String(), fmt.Errorf("make %s failed", target)
	}
	return sb.String(), nil
}

func appendSessionNote(projectRoot, summary, nextSteps string) (string, error) {
	root, err := resolveProjectRoot(projectRoot)
	if err != nil {
		return "", err
	}

	sessionPath := filepath.Join(root, "session.md")
	f, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return "", fmt.Errorf("open session.md: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	sb.WriteString("\n\n### ")
	sb.WriteString(time.Now().Format("2006-01-02 15:04 MST"))
	sb.WriteString("\n\n")
	sb.WriteString("**Summary**\n")
	sb.WriteString(summary)
	sb.WriteString("\n")
	if strings.TrimSpace(nextSteps) != "" {
		sb.WriteString("\n**Next steps**\n")
		sb.WriteString(nextSteps)
		sb.WriteString("\n")
	}

	if _, err := f.WriteString(sb.String()); err != nil {
		return "", fmt.Errorf("write session.md: %w", err)
	}
	return fmt.Sprintf("updated %s", sessionPath), nil
}
