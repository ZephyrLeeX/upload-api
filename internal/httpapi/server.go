package httpapi

import (
	"log/slog"
	"net/http"
	"sync"

	"upload-api/internal/config"
	"upload-api/internal/storage"
)

type Server struct {
	cfg       config.Config
	storage   *storage.Storage
	logger    *slog.Logger
	semaphore chan struct{}
	filenames sync.Map
	diskFree  func(string) (uint64, error)
}

func New(cfg config.Config, store *storage.Storage, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg: cfg, storage: store, logger: logger,
		semaphore: make(chan struct{}, cfg.MaxConcurrent),
		diskFree:  storage.AvailableBytes,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("PUT /api/v1/upload/{filename}", s.upload)
	return s.logRequests(mux)
}
