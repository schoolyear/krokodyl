package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListStagedFilesNestedTree(t *testing.T) {
	staging := t.TempDir()
	writeFile(t, filepath.Join(staging, "top.txt"), "top")
	writeFile(t, filepath.Join(staging, "dir", "nested.txt"), "nested")
	writeFile(t, filepath.Join(staging, "dir", "deeper", "leaf.txt"), "leaf")

	files, err := listStagedFiles(staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}

	rels := make(map[string]bool)
	for _, f := range files {
		rels[filepath.ToSlash(f.RelPath)] = true
	}
	for _, want := range []string{"top.txt", "dir/nested.txt", "dir/deeper/leaf.txt"} {
		if !rels[want] {
			t.Errorf("missing staged file %s, got %v", want, rels)
		}
	}
}

func TestMoveStagedFilePreservesNestedPath(t *testing.T) {
	staging := t.TempDir()
	dest := t.TempDir()
	writeFile(t, filepath.Join(staging, "dir", "deeper", "leaf.txt"), "leaf")

	rel := filepath.Join("dir", "deeper", "leaf.txt")
	if err := moveStagedFile(staging, dest, rel, nil); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "dir", "deeper", "leaf.txt"))
	if err != nil {
		t.Fatalf("file not moved to nested destination: %v", err)
	}
	if string(got) != "leaf" {
		t.Errorf("content mismatch: %q", got)
	}
	if _, err := os.Stat(filepath.Join(staging, rel)); !os.IsNotExist(err) {
		t.Error("source file still present after move")
	}
}

func TestMoveStagedFileFallsBackToCopyOnRenameFailure(t *testing.T) {
	staging := t.TempDir()
	dest := t.TempDir()
	writeFile(t, filepath.Join(staging, "big.bin"), "payload")

	// Simulate EXDEV: rename always fails, copy path must take over.
	failingRename := func(_, _ string) error { return errors.New("cross-device link") }

	if err := moveStagedFile(staging, dest, "big.bin", failingRename); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "big.bin"))
	if err != nil {
		t.Fatalf("copy fallback did not produce destination file: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("content mismatch after copy fallback: %q", got)
	}
}

func TestMoveStagedFileMissingSourceFails(t *testing.T) {
	staging := t.TempDir()
	dest := t.TempDir()

	failingRename := func(_, _ string) error { return errors.New("no such file") }
	if err := moveStagedFile(staging, dest, "ghost.txt", failingRename); err == nil {
		t.Error("expected error for missing source file")
	}
}

func TestMoveStagedFileRejectsUnsafePaths(t *testing.T) {
	staging := t.TempDir()
	dest := t.TempDir()

	// File names come from the remote sender — traversal must be rejected
	// before any filesystem operation happens.
	unsafe := []string{
		filepath.Join("..", "escape.txt"),
		filepath.Join("..", "..", "etc", "cron.d", "evil"),
		filepath.Join("dir", "..", "..", "escape.txt"),
	}
	for _, rel := range unsafe {
		t.Run(rel, func(t *testing.T) {
			if err := moveStagedFile(staging, dest, rel, nil); err == nil {
				t.Errorf("unsafe path %q was not rejected", rel)
			}
		})
	}
}

func TestStagingDirForCodeDeterministicAndScoped(t *testing.T) {
	dest := `D:\Downloads`
	a := stagingDirForCode(dest, "1671-salt-sphere-monaco")
	b := stagingDirForCode(dest, "1671-salt-sphere-monaco")
	c := stagingDirForCode(dest, "9999-other-code-word")

	if a != b {
		t.Errorf("same code must yield the same staging dir: %q != %q", a, b)
	}
	if a == c {
		t.Error("different codes must yield different staging dirs")
	}
	if filepath.Dir(a) != filepath.Clean(dest) {
		t.Errorf("staging dir must live under the destination, got %q", a)
	}
	if strings.Contains(filepath.Base(a), "salt-sphere") {
		t.Error("staging dir name must not leak the code")
	}
}

func TestValidateRelPath(t *testing.T) {
	tests := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{"plain file ok", "file.txt", false},
		{"nested file ok", filepath.Join("dir", "file.txt"), false},
		{"dotfile ok", ".hidden", false},
		{"parent escape rejected", "..", true},
		{"traversal rejected", filepath.Join("..", "x.txt"), true},
		{"nested traversal rejected", filepath.Join("a", "..", "..", "x.txt"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRelPath(tt.rel)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRelPath(%q) error = %v, wantErr %v", tt.rel, err, tt.wantErr)
			}
		})
	}
}

func TestUnsafeWindowsSegment(t *testing.T) {
	tests := []struct {
		seg    string
		unsafe bool
	}{
		{"file.txt", false},
		{"console.log", false}, // prefix of a reserved name is fine
		{"com10.txt", false},   // only COM1-9 are reserved
		{"CON", true},
		{"con", true},
		{"Con.txt", true},   // reservation survives an extension
		{" con.txt", false}, // leading space: NOT a device name on Windows
		{"con .txt", true},  // trailing space in the stem still matches CON
		{"NUL", true},
		{"nul.dat", true},
		{"COM1", true},
		{"lpt9.log", true},
		{"trailing.", true}, // Win32 strips trailing dots
		{"trailing ", true}, // ... and trailing spaces
		{"normal name with spaces", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.seg, func(t *testing.T) {
			if got := unsafeWindowsSegment(tt.seg); got != tt.unsafe {
				t.Errorf("unsafeWindowsSegment(%q) = %v, want %v", tt.seg, got, tt.unsafe)
			}
		})
	}
}

func TestValidateRelPathWindowsThreats(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only validation rules")
	}
	tests := []struct {
		name string
		rel  string
	}{
		{"reserved device name", "CON"},
		{"reserved name nested", filepath.Join("sub", "NUL.txt")},
		{"alternate data stream", "report.txt:hidden"},
		{"trailing dot", "report."},
		{"trailing space", "report "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateRelPath(tt.rel); err == nil {
				t.Errorf("validateRelPath(%q) accepted an unsafe path", tt.rel)
			}
		})
	}
}

func TestCopyFileLeavesNoTempAndReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	writeFile(t, src, "new content")
	writeFile(t, dst, "old content") // pre-existing destination survives until the copy is whole

	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new content" {
		t.Errorf("dst = %q, want %q", got, "new content")
	}
	if _, err := os.Stat(dst + ".krokodyl-tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Error("temp file left behind after a successful copy")
	}
}
