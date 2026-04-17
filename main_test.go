package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func mustTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcp-fs-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func withRoots(t *testing.T, roots []string) {
	t.Helper()
	rootsMu.Lock()
	allowedRoots = roots
	rootsMu.Unlock()
	t.Cleanup(func() {
		rootsMu.Lock()
		allowedRoots = nil
		rootsMu.Unlock()
	})
}

// ── resolvePath ───────────────────────────────────────────────────────────────

func TestResolvePath(t *testing.T) {
	root := mustTempDir(t)
	subdir := filepath.Join(root, "sub")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	withRoots(t, []string{root})

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"exact root", root, false},
		{"subdir", subdir, false},
		{"file inside root", filepath.Join(root, "file.txt"), false},
		{"outside root", "/tmp", true},
		{"path traversal attempt", filepath.Join(root, "..", "etc"), true},
		{"empty string resolves to cwd", "", true}, // cwd unlikely to be under temp root
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolvePath(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// ── setRoots ─────────────────────────────────────────────────────────────────────

func TestSetRoots(t *testing.T) {
	validDir := mustTempDir(t)

	// Create a temp file to test file-not-dir rejection
	tmpFile, err := os.CreateTemp("", "mcp-fs-file-*")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	tests := []struct {
		name    string
		roots   []string
		wantErr bool
	}{
		{"valid directory", []string{validDir}, false},
		{"empty list clears roots", []string{}, false},
		{"nonexistent path", []string{"/does/not/exist/ever"}, true},
		{"file instead of dir", []string{tmpFile.Name()}, true},
		{"mix valid and invalid", []string{validDir, "/nope"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := setRoots(tc.roots)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// ── skipDirs ─────────────────────────────────────────────────────────────────────

func TestSkipDirs(t *testing.T) {
	should := []string{
		".git", ".svn", ".hg",
		"node_modules", "vendor",
		".venv", "venv", "__pycache__",
		"dist", "build", ".next", ".nuxt",
		".turbo", ".cache",
	}
	should_not := []string{
		"src", "cmd", "pkg", "internal", "main.go", ".env",
	}

	for _, d := range should {
		if !skipDirs[d] {
			t.Errorf("skipDirs missing %q", d)
		}
	}
	for _, d := range should_not {
		if skipDirs[d] {
			t.Errorf("skipDirs incorrectly contains %q", d)
		}
	}
}

// ── patch logic ─────────────────────────────────────────────────────────────────

func TestPatchLogic(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		old      string
		new      string
		want     string
		wantMiss bool
	}{
		{
			name: "simple replace",
			src:  "hello world",
			old:  "world",
			new:  "Go",
			want: "hello Go",
		},
		{
			name: "only first occurrence replaced",
			src:  "aaa",
			old:  "a",
			new:  "b",
			want: "baa",
		},
		{
			name:     "old not found",
			src:      "hello world",
			old:      "missing",
			new:      "x",
			wantMiss: true,
		},
		{
			name: "multiline patch",
			src:  "line1\nline2\nline3",
			old:  "line2",
			new:  "LINE2",
			want: "line1\nLINE2\nline3",
		},
		{
			name: "replace with empty string",
			src:  "remove this please",
			old:  " this",
			new:  "",
			want: "remove please",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.src, tc.old) {
				if !tc.wantMiss {
					t.Fatalf("test setup error: old_str not in src")
				}
				return
			}
			if tc.wantMiss {
				t.Errorf("expected old_str to be missing but it was found")
				return
			}
			got := strings.Replace(tc.src, tc.old, tc.new, 1)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ── list_directory skip integration ─────────────────────────────────────────────

func TestListDirectorySkipsNoiseDirs(t *testing.T) {
	root := mustTempDir(t)
	withRoots(t, []string{root})

	// Create dirs that should be skipped and one that should appear
	for _, d := range []string{".git", "node_modules", "src"} {
		if err := os.Mkdir(filepath.Join(root, d), 0755); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	var visible []string
	for _, e := range entries {
		if e.IsDir() && skipDirs[e.Name()] {
			continue
		}
		visible = append(visible, e.Name())
	}

	if len(visible) != 1 || visible[0] != "src" {
		t.Errorf("expected only [src], got %v", visible)
	}
}
