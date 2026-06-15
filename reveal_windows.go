//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
)

// revealPath opens Windows Explorer with the file selected. explorer.exe returns
// exit code 1 even on success, so the error is intentionally ignored.
func revealPath(path string) error {
	_ = exec.Command("explorer", "/select,"+filepath.FromSlash(path)).Run()
	return nil
}
