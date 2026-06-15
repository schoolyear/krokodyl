//go:build !windows

package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// revealPath shows a file in the platform's file manager: macOS selects it,
// other platforms open its containing directory.
func revealPath(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Run()
	}
	return exec.Command("xdg-open", filepath.Dir(path)).Run()
}
