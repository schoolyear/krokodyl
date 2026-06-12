package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// partialDirPrefix marks krokodyl's staging directories so cleanup can never
// touch anything else.
const partialDirPrefix = ".krokodyl-partial-"

// stagingDirForCode derives a deterministic staging directory from the
// transfer code, inside the destination (kept on the same volume so the final
// rename never crosses devices). Deterministic-per-code is what makes resume
// work: a retry with the same code lands on the same partial bytes, and croc
// resumes the missing chunks. The code is hashed so the directory name never
// leaks the shared secret.
func stagingDirForCode(destinationPath, code string) string {
	sum := sha256.Sum256([]byte(code))
	return filepath.Join(destinationPath, partialDirPrefix+hex.EncodeToString(sum[:8]))
}

// stagedFile describes one received file inside a staging directory,
// addressed by its path relative to the staging root so nested folder
// structures survive the move to the destination.
type stagedFile struct {
	RelPath string
	Size    int64
	ModTime time.Time
}

// listStagedFiles walks stagingDir recursively and returns all regular files
// with staging-relative paths.
func listStagedFiles(stagingDir string) ([]stagedFile, error) {
	var files []stagedFile
	err := filepath.WalkDir(stagingDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(stagingDir, path)
		if err != nil {
			return err
		}
		if err := validateRelPath(rel); err != nil {
			return err
		}
		files = append(files, stagedFile{
			RelPath: rel,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list staged files in %s: %w", stagingDir, err)
	}
	return files, nil
}

// validateRelPath rejects staged paths that could escape their root. File
// names ultimately come from the remote sender via croc, so they are
// untrusted input.
func validateRelPath(rel string) error {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) ||
		clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("received file has an unsafe path: %q", rel)
	}
	if runtime.GOOS == "windows" {
		// Colons enable drive-letter and NTFS alternate-data-stream tricks.
		if strings.Contains(clean, ":") {
			return fmt.Errorf("received file has an unsafe path: %q", rel)
		}
		for _, seg := range strings.Split(clean, string(filepath.Separator)) {
			if unsafeWindowsSegment(seg) {
				return fmt.Errorf("received file has an unsafe path: %q", rel)
			}
		}
	}
	return nil
}

// windowsReservedNames are device names Win32 reserves in every directory;
// creating them opens the device instead of a file (NUL silently swallows
// data, CON blocks). The reservation applies to the name with any extension
// stripped ("nul.txt" is still NUL).
var windowsReservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// unsafeWindowsSegment reports whether one path segment is dangerous on
// Windows: a reserved device name, or a name with trailing dots/spaces
// (Win32 silently strips those, so the file created would not be the path
// that was validated). Pure so it is testable on every platform.
func unsafeWindowsSegment(seg string) bool {
	if seg == "" {
		return false
	}
	if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
		return true
	}
	base := seg
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	return windowsReservedNames[strings.ToLower(strings.TrimSpace(base))]
}

// renameFunc is injected so tests can simulate cross-device rename failures.
type renameFunc func(oldpath, newpath string) error

// moveStagedFile moves one staged file to destDir, creating parent
// directories as needed. When rename fails (e.g. staging and destination end
// up on different volumes), it falls back to copy+delete so the transfer
// still completes.
func moveStagedFile(stagingDir, destDir, relPath string, rename renameFunc) error {
	if rename == nil {
		rename = os.Rename
	}

	if err := validateRelPath(relPath); err != nil {
		return err
	}

	src := filepath.Join(stagingDir, relPath)
	dst := filepath.Join(destDir, relPath)
	if !strings.HasPrefix(dst, filepath.Clean(destDir)+string(filepath.Separator)) {
		return fmt.Errorf("refusing to write outside the destination directory: %q", relPath)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("failed to create destination directory for %s: %w", relPath, err)
	}

	if err := rename(src, dst); err == nil {
		return nil
	}

	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("failed to move %s to destination: %w", relPath, err)
	}
	if err := os.Remove(src); err != nil {
		// The copy succeeded; a leftover staging file is cleaned with the
		// staging dir later. Not fatal.
		return nil
	}
	return nil
}

// copyFile copies via a temp file in the destination directory and renames
// into place, so a crash mid-copy can never leave a truncated file that looks
// complete at dst (and an existing dst survives until the copy is whole).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".krokodyl-tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
