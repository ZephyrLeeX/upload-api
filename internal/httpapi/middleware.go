package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"upload-api/internal/requestid"
)

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}

type requestMetaKey struct{}

type requestMeta struct {
	id, filename, sha256, result, errorCode string
	actualBytes                             int64
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		meta := &requestMeta{id: requestid.New(), result: "success"}
		r = r.WithContext(context.WithValue(r.Context(), requestMetaKey{}, meta))
		tracked := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(tracked, r)
		if tracked.status == 0 {
			tracked.status = http.StatusOK
		}
		if tracked.status >= 400 && meta.errorCode == "" {
			meta.result, meta.errorCode = "failed", "http_error"
		}
		s.logger.LogAttrs(r.Context(), levelForStatus(tracked.status), "http request",
			slog.String("request_id", meta.id), slog.String("remote_addr", r.RemoteAddr),
			slog.String("method", r.Method), slog.String("path", r.URL.Path),
			slog.String("filename", meta.filename), slog.Int64("content_length", r.ContentLength),
			slog.Int64("actual_bytes", meta.actualBytes), slog.String("sha256", meta.sha256),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()), slog.Int("status", tracked.status),
			slog.String("result", meta.result), slog.String("error_code", meta.errorCode))
	})
}

func levelForStatus(status int) slog.Level {
	if status >= 500 {
		return slog.LevelError
	}
	if status >= 400 {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}
