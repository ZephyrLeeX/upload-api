package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var ErrExists = errors.New("file already exists")

type Storage struct {
	dir    string
	tmpDir string
}

func New(dir string) (*Storage, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve storage directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0750); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}
	tmp := filepath.Join(abs, ".tmp")
	if err := os.MkdirAll(tmp, 0750); err != nil {
		return nil, fmt.Errorf("create temporary directory: %w", err)
	}
	probe, err := os.CreateTemp(tmp, ".write-test-*")
	if err != nil {
		return nil, fmt.Errorf("storage directory is not writable: %w", err)
	}
	probeName := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(probeName)
		return nil, fmt.Errorf("close storage probe: %w", err)
	}
	if err := os.Remove(probeName); err != nil {
		return nil, fmt.Errorf("remove storage probe: %w", err)
	}
	return &Storage{dir: abs, tmpDir: tmp}, nil
}

func ValidateFilename(name string) error {
	if name == "" || name == "." || name == ".." {
		return errors.New("filename is empty or reserved")
	}
	if !utf8.ValidString(name) || len([]byte(name)) > 255 {
		return errors.New("filename must be valid UTF-8 and at most 255 bytes")
	}
	if strings.ContainsAny(name, "/\\") {
		return errors.New("filename must not contain path separators")
	}
	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return errors.New("filename must not end with a space or dot")
	}
	for _, r := range name {
		if r <= 0x1f || r == 0x7f {
			return errors.New("filename must not contain control characters")
		}
	}
	return nil
}

func (s *Storage) Exists(name string) (bool, error) {
	_, err := os.Lstat(filepath.Join(s.dir, name))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (s *Storage) CreateTemp(requestID string) (*os.File, error) {
	return os.CreateTemp(s.tmpDir, requestID+"-*.tmp")
}

// Commit atomically publishes a completed file without ever replacing an existing file.
func (s *Storage) Commit(tempPath, name string) error {
	finalPath := filepath.Join(s.dir, name)
	if err := os.Link(tempPath, finalPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrExists
		}
		return fmt.Errorf("publish file: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		if removeErr := os.Remove(finalPath); removeErr != nil {
			return fmt.Errorf("remove temporary link: %w (rollback failed: %v)", err, removeErr)
		}
		return fmt.Errorf("remove temporary link: %w", err)
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("open storage directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		if removeErr := os.Remove(finalPath); removeErr != nil {
			return fmt.Errorf("sync storage directory: %w (rollback failed: %v)", err, removeErr)
		}
		return fmt.Errorf("sync storage directory: %w", err)
	}
	return nil
}

func (s *Storage) RemoveTemp(path string) error {
	if filepath.Dir(path) != s.tmpDir || filepath.Ext(path) != ".tmp" {
		return errors.New("refusing to remove path outside temporary directory")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Storage) Dir() string { return s.dir }
