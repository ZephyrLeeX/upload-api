package storage

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

func (s *Storage) CleanupExpired(now time.Time, ttl time.Duration) error {
	entries, err := os.ReadDir(s.tmpDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".tmp" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		if now.Sub(info.ModTime()) <= ttl {
			continue
		}
		if err := os.Remove(filepath.Join(s.tmpDir, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Storage) RunCleanup(ctx context.Context, ttl, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := s.CleanupExpired(now, ttl); err != nil {
				logger.Error("temporary file cleanup failed", "error", err)
			}
		}
	}
}
