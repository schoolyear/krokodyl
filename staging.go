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
	// Colons enable drive-letter and NTFS alternate-data-stream tricks.
	if runtime.GOOS == "windows" && strings.Contains(clean, ":") {
		return fmt.Errorf("received file has an unsafe path: %q", rel)
	}
	return nil
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

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
