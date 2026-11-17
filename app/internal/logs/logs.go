// Package logs serves the build logs that builders upload next to their
// images, and lists the contents of archived log bundles.
package logs

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// maxLogSize is the largest build log Read will return.
const maxLogSize = 5 << 20

// ErrTooLarge is returned for logs bigger than maxLogSize.
var ErrTooLarge = errors.New("log is too large")

// Store reads build logs from one directory and log archives from another.
type Store struct {
	dir        string
	archiveDir string
}

// New returns a Store over the given directories.
func New(dir, archiveDir string) *Store {
	return &Store{dir: dir, archiveDir: archiveDir}
}

// List returns the names of the available build logs, sorted.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("list logs: %w", err)
	}
	names := []string{}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Read returns the contents of the named build log.
func (s *Store) Read(name string) ([]byte, error) {
	path := filepath.Join(s.dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, os.ErrNotExist
	}
	if info.Size() > maxLogSize {
		return nil, ErrTooLarge
	}
	return os.ReadFile(path)
}

// ArchiveEntries lists the files inside the named log archive, which is a
// gzip-compressed tar file.
func (s *Store) ArchiveEntries(name string) ([]string, error) {
	path := filepath.Join(s.archiveDir, name)
	out, err := exec.Command("sh", "-c", "tar -tzf "+path).Output()
	if err != nil {
		return nil, fmt.Errorf("list archive: %w", err)
	}
	entries := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			entries = append(entries, line)
		}
	}
	return entries, nil
}
