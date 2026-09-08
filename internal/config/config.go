package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr    string
	StorageDir    string
	MaxFileSize   int64
	MaxConcurrent int
	MinFreeSpace  uint64
	TempFileTTL   time.Duration
	UploadToken   string
	TLSCertFile   string
	TLSKeyFile    string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:  envOr("LISTEN_ADDR", ":10443"),
		StorageDir:  envOr("STORAGE_DIR", "/data/uploads"),
		UploadToken: os.Getenv("UPLOAD_TOKEN"),
		TLSCertFile: envOr("TLS_CERT_FILE", "/etc/upload-api/tls/server.crt"),
		TLSKeyFile:  envOr("TLS_KEY_FILE", "/etc/upload-api/tls/server.key"),
	}
	var err error
	if cfg.MaxFileSize, err = int64Env("MAX_FILE_SIZE", 20*1024*1024*1024); err != nil {
		return Config{}, err
	}
	if cfg.MaxConcurrent, err = intEnv("MAX_CONCURRENT_UPLOADS", 2); err != nil {
		return Config{}, err
	}
	if cfg.MinFreeSpace, err = uint64Env("MIN_FREE_SPACE", 1024*1024*1024); err != nil {
		return Config{}, err
	}
	if cfg.TempFileTTL, err = durationEnv("TEMP_FILE_TTL", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if len(c.UploadToken) < 32 {
		return errors.New("UPLOAD_TOKEN must contain at least 32 characters")
	}
	if c.MaxFileSize <= 0 {
		return errors.New("MAX_FILE_SIZE must be greater than zero")
	}
	if c.MaxConcurrent <= 0 {
		return errors.New("MAX_CONCURRENT_UPLOADS must be greater than zero")
	}
	if c.TempFileTTL <= 0 {
		return errors.New("TEMP_FILE_TTL must be greater than zero")
	}
	if c.StorageDir == "" || c.ListenAddr == "" {
		return errors.New("LISTEN_ADDR and STORAGE_DIR must not be empty")
	}
	if err := regularFile(c.TLSCertFile, "TLS_CERT_FILE"); err != nil {
		return err
	}
	return regularFile(c.TLSKeyFile, "TLS_KEY_FILE")
}

func regularFile(path, name string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s is not accessible: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must reference a regular file", name)
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func int64Env(name string, fallback int64) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func intEnv(name string, fallback int) (int, error) {
	value, err := int64Env(name, int64(fallback))
	if err != nil {
		return 0, err
	}
	if int64(int(value)) != value {
		return 0, fmt.Errorf("%s is out of range", name)
	}
	return int(value), nil
}

func uint64Env(name string, fallback uint64) (uint64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a non-negative integer: %w", name, err)
	}
	return parsed, nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	return parsed, nil
}
