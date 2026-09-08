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
	s, err := New(t.TempDir())
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

func TestCleanupExpiredOnlyRemovesTmpFiles(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldTmp := filepath.Join(s.tmpDir, "old.tmp")
	keep := filepath.Join(s.tmpDir, "keep.txt")
	if err := os.WriteFile(oldTmp, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, nil, 0600); err != nil {
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
}
