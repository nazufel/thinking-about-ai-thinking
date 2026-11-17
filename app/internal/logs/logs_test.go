package logs

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func newTestStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "logs")
	archiveDir := filepath.Join(root, "archives")
	for _, d := range []string{dir, archiveDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return New(dir, archiveDir), dir, archiveDir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeArchive creates a .tar.gz at path holding one empty file per name.
func writeArchive(t *testing.T, path string, names ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 0, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestList(t *testing.T) {
	s, dir, _ := newTestStore(t)
	writeFile(t, filepath.Join(dir, "build-2.log"), "two")
	writeFile(t, filepath.Join(dir, "build-1.log"), "one")
	writeFile(t, filepath.Join(dir, "notes.txt"), "not a log")

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"build-1.log", "build-2.log"}; !reflect.DeepEqual(got, want) {
		t.Errorf("List() = %v, want %v", got, want)
	}
}

func TestRead(t *testing.T) {
	s, dir, _ := newTestStore(t)
	writeFile(t, filepath.Join(dir, "build-1.log"), "step 1 ok\n")

	got, err := s.Read("build-1.log")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "step 1 ok\n" {
		t.Errorf("Read = %q, want the log contents", got)
	}
	if _, err := s.Read("missing.log"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Read of a missing log returned %v, want os.ErrNotExist", err)
	}
}

func TestArchiveEntries(t *testing.T) {
	s, _, archiveDir := newTestStore(t)
	writeArchive(t, filepath.Join(archiveDir, "2026-02.tar.gz"), "build-7.log", "build-8.log")

	got, err := s.ArchiveEntries("2026-02.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"build-7.log", "build-8.log"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ArchiveEntries = %v, want %v", got, want)
	}
	if _, err := s.ArchiveEntries("missing.tar.gz"); err == nil {
		t.Error("ArchiveEntries of a missing archive returned no error")
	}
}
