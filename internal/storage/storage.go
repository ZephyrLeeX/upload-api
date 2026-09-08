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

func New(dir, tmpDir string) (*Storage, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve storage directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0750); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}
	tmpAbs, err := filepath.Abs(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("resolve temporary directory: %w", err)
	}
	if err := os.MkdirAll(tmpAbs, 0750); err != nil {
		return nil, fmt.Errorf("create temporary directory: %w", err)
	}
	dirInfo, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat storage directory: %w", err)
	}
	tmpInfo, err := os.Stat(tmpAbs)
	if err != nil {
		return nil, fmt.Errorf("stat temporary directory: %w", err)
	}
	if os.SameFile(dirInfo, tmpInfo) {
		return nil, errors.New("TMP_DIR and STORAGE_DIR must be different directories")
	}
	same, err := sameFilesystem(abs, tmpAbs)
	if err != nil {
		return nil, fmt.Errorf("compare storage filesystems: %w", err)
	}
	if !same {
		return nil, errors.New("TMP_DIR and STORAGE_DIR must be on the same filesystem")
	}
	if err := verifyPublish(abs, tmpAbs); err != nil {
		return nil, err
	}
	return &Storage{dir: abs, tmpDir: tmpAbs}, nil
}

func verifyPublish(dir, tmpDir string) error {
	tmpProbe, err := os.CreateTemp(tmpDir, ".publish-test-*.tmp")
	if err != nil {
		return fmt.Errorf("create storage probe: %w", err)
	}
	tmpPath := tmpProbe.Name()
	finalPath := ""
	defer func() {
		_ = tmpProbe.Close()
		if finalPath != "" {
			_ = os.Remove(finalPath)
		}
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpProbe.Write([]byte("probe")); err != nil {
		return fmt.Errorf("write storage probe: %w", err)
	}
	if err := tmpProbe.Close(); err != nil {
		return fmt.Errorf("close storage probe: %w", err)
	}

	finalProbe, err := os.CreateTemp(dir, ".publish-test-*")
	if err != nil {
		return fmt.Errorf("create final storage probe name: %w", err)
	}
	finalPath = finalProbe.Name()
	if err := finalProbe.Close(); err != nil {
		return fmt.Errorf("close final storage probe: %w", err)
	}
	if err := os.Remove(finalPath); err != nil {
		return fmt.Errorf("prepare final storage probe: %w", err)
	}
	if err := os.Link(tmpPath, finalPath); err != nil {
		return fmt.Errorf("storage directory cannot publish files: %w", err)
	}
	if err := os.Remove(finalPath); err != nil {
		return fmt.Errorf("remove final storage probe: %w", err)
	}
	finalPath = ""
	if err := os.Remove(tmpPath); err != nil {
		return fmt.Errorf("remove temporary storage probe: %w", err)
	}
	tmpPath = ""
	return nil
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
