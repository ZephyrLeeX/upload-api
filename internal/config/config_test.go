package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func configEnvironment(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cert := filepath.Join(dir, "server.crt")
	key := filepath.Join(dir, "server.key")
	for _, path := range []string{cert, key} {
		if err := os.WriteFile(path, []byte("test fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{
		"LISTEN_ADDR", "STORAGE_DIR", "MAX_FILE_SIZE", "MAX_CONCURRENT_UPLOADS",
		"MIN_FREE_SPACE", "TEMP_FILE_TTL", "SHUTDOWN_TIMEOUT",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("UPLOAD_TOKEN", "0123456789abcdef0123456789abcdef")
	t.Setenv("TLS_CERT_FILE", cert)
	t.Setenv("TLS_KEY_FILE", key)
}

func TestLoadDefaults(t *testing.T) {
	configEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrent != 1 {
		t.Fatalf("MaxConcurrent = %d, want 1", cfg.MaxConcurrent)
	}
	if cfg.ShutdownTimeout != 2*time.Hour {
		t.Fatalf("ShutdownTimeout = %v, want 2h", cfg.ShutdownTimeout)
	}
}

func TestLoadShutdownTimeout(t *testing.T) {
	configEnvironment(t)
	t.Setenv("SHUTDOWN_TIMEOUT", "2h30m")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ShutdownTimeout != 2*time.Hour+30*time.Minute {
		t.Fatalf("ShutdownTimeout = %v, want 2h30m", cfg.ShutdownTimeout)
	}
}

func TestLoadRejectsNonPositiveShutdownTimeout(t *testing.T) {
	for _, value := range []string{"0s", "-1s"} {
		t.Run(value, func(t *testing.T) {
			configEnvironment(t)
			t.Setenv("SHUTDOWN_TIMEOUT", value)
			if _, err := Load(); err == nil {
				t.Fatal("Load accepted non-positive SHUTDOWN_TIMEOUT")
			}
		})
	}
}
