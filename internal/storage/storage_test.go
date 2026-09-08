package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateFilename(t *testing.T) {
	valid := []string{"abc.zip", "data-01.tar.gz", "测试文件.txt"}
	for _, name := range valid {
		if err := ValidateFilename(name); err != nil {
			t.Errorf("%q rejected: %v", name, err)
		}
	}
	invalid := []string{"", ".", "..", "../abc", "a/b", `a\b`, "bad\x00name", "bad\x1fname", "trailing.", "trailing "}
	for _, name := range invalid {
		if err := ValidateFilename(name); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
	if err := ValidateFilename(strings.Repeat("界", 86)); err == nil {
		t.Error("filename longer than 255 UTF-8 bytes accepted")
	}
}

func TestCommitDoesNotOverwrite(t *testing.T) {
	s, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "same.bin"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	tmp, err := s.CreateTemp("request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(tmp.Name(), "same.bin"); err != ErrExists {
		t.Fatalf("got %v, want ErrExists", err)
	}
	data, err := os.ReadFile(filepath.Join(s.Dir(), "same.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("existing file changed to %q", data)
	}
}

func TestNewChecksPublishAndLeavesNoProbes(t *testing.T) {
	dir := t.TempDir()
	tmpDir := t.TempDir()
	existingPath := filepath.Join(dir, "existing.bin")
	if err := os.WriteFile(existingPath, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := New(dir, tmpDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "unchanged" {
		t.Fatalf("existing file changed to %q", data)
	}
	for _, probeDir := range []string{dir, tmpDir} {
		entries, err := os.ReadDir(probeDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".publish-test-") {
				t.Fatalf("probe remains in %s: %s", probeDir, entry.Name())
			}
		}
	}
}

func TestCleanupExpiredOnlyRemovesTmpFiles(t *testing.T) {
	dir := t.TempDir()
	tmpDir := t.TempDir()
	s, err := New(dir, tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	oldTmp := filepath.Join(s.tmpDir, "old.tmp")
	keep := filepath.Join(s.tmpDir, "keep.txt")
	final := filepath.Join(dir, "final.bin")
	if err := os.WriteFile(oldTmp, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("final"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldTmp, old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupExpired(time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldTmp); !os.IsNotExist(err) {
		t.Fatalf("expired tmp still exists: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("non-tmp file removed: %v", err)
	}
	if data, err := os.ReadFile(final); err != nil || string(data) != "final" {
		t.Fatalf("final file was modified: data=%q err=%v", data, err)
	}
}

func TestCreateTempUsesConfiguredTmpDir(t *testing.T) {
	s, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := s.CreateTemp("request")
	if err != nil {
		t.Fatal(err)
	}
	path := tmp.Name()
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if filepath.Dir(path) != s.tmpDir {
		t.Fatalf("temporary file created in %q, want %q", filepath.Dir(path), s.tmpDir)
	}
}

func TestNewRejectsSameDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(dir, dir); err == nil || !strings.Contains(err.Error(), "must be different directories") {
		t.Fatalf("New error = %v, want different-directories error", err)
	}
}

func TestNewRejectsDifferentFilesystemsWhenAvailable(t *testing.T) {
	storageDir := t.TempDir()
	tmpDir, err := os.MkdirTemp("/dev/shm", "upload-api-test-")
	if err != nil {
		t.Skipf("cannot create a test directory on /dev/shm: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	same, err := sameFilesystem(storageDir, tmpDir)
	if err != nil {
		t.Skipf("cannot compare test filesystems: %v", err)
	}
	if same {
		t.Skip("temporary test directories are on the same filesystem")
	}
	if _, err := New(storageDir, tmpDir); err == nil || !strings.Contains(err.Error(), "must be on the same filesystem") {
		t.Fatalf("New error = %v, want same-filesystem error", err)
	}
}
