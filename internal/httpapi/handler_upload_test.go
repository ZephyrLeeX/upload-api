package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"upload-api/internal/config"
	"upload-api/internal/storage"
)

const testToken = "0123456789abcdef0123456789abcdef"

func newTestServer(t *testing.T, concurrency int) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{StorageDir: dir, MaxFileSize: 1024, MaxConcurrent: concurrency, UploadToken: testToken}
	s := New(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.diskFree = func(string) (uint64, error) { return 1 << 40, nil }
	return s, dir
}

func uploadRequest(body io.Reader, length int64, filename string) *http.Request {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/upload/file.bin", body)
	req.SetPathValue("filename", filename)
	req.ContentLength = length
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	return req
}

func TestParseSHA256(t *testing.T) {
	valid := strings.Repeat("aB", 32)
	got, err := ParseSHA256(valid)
	if err != nil || got != strings.ToLower(valid) {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, value := range []string{"abc", strings.Repeat("z", 64)} {
		if _, err := ParseSHA256(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestUploadValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{"missing token", func(r *http.Request) { r.Header.Del("Authorization") }, 401},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, 401},
		{"missing length", func(r *http.Request) { r.ContentLength = -1 }, 411},
		{"too large", func(r *http.Request) { r.ContentLength = 1025 }, 413},
		{"invalid filename", func(r *http.Request) { r.SetPathValue("filename", "../x") }, 400},
		{"invalid content type", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestServer(t, 1)
			req := uploadRequest(bytes.NewReader([]byte("x")), 1, "file.bin")
			tt.mutate(req)
			rr := httptest.NewRecorder()
			s.upload(rr, req)
			if rr.Code != tt.want {
				t.Fatalf("got %d body=%s, want %d", rr.Code, rr.Body, tt.want)
			}
		})
	}
}

func TestUploadSuccessAndSHA(t *testing.T) {
	s, dir := newTestServer(t, 1)
	body := []byte("hello upload")
	sum := sha256.Sum256(body)
	req := uploadRequest(bytes.NewReader(body), int64(len(body)), "测试.bin")
	req.Header.Set("X-File-SHA256", strings.ToUpper(hex.EncodeToString(sum[:])))
	rr := httptest.NewRecorder()
	s.upload(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", rr.Code, rr.Body)
	}
	var response uploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SHA256 != hex.EncodeToString(sum[:]) || response.Size != int64(len(body)) {
		t.Fatalf("unexpected response: %+v", response)
	}
	got, err := os.ReadFile(filepath.Join(dir, "测试.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("stored content differs")
	}
}

func TestUploadFailuresLeaveNoFiles(t *testing.T) {
	tests := []struct {
		name   string
		body   []byte
		length int64
		hash   string
		want   int
	}{
		{"sha mismatch", []byte("hello"), 5, strings.Repeat("0", 64), 422},
		{"short body", []byte("hi"), 5, "", 422},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := newTestServer(t, 1)
			req := uploadRequest(bytes.NewReader(tt.body), tt.length, "failed.bin")
			req.Header.Set("X-File-SHA256", tt.hash)
			rr := httptest.NewRecorder()
			s.upload(rr, req)
			if rr.Code != tt.want {
				t.Fatalf("got %d: %s", rr.Code, rr.Body)
			}
			assertNoUploadArtifacts(t, dir)
		})
	}
}

func TestUploadExistingFile(t *testing.T) {
	s, dir := newTestServer(t, 1)
	if err := os.WriteFile(filepath.Join(dir, "file.bin"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.upload(rr, uploadRequest(bytes.NewReader([]byte("new")), 3, "file.bin"))
	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d: %s", rr.Code, rr.Body)
	}
}

func TestCanceledUploadCleansTemporaryFile(t *testing.T) {
	s, dir := newTestServer(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := uploadRequest(bytes.NewReader([]byte("hello")), 5, "cancel.bin").WithContext(ctx)
	req.SetPathValue("filename", "cancel.bin")
	rr := httptest.NewRecorder()
	s.upload(rr, req)
	assertNoUploadArtifacts(t, dir)
}

type blockingReader struct {
	entered chan struct{}
	release chan struct{}
}

func (r *blockingReader) Read(p []byte) (int, error) { close(r.entered); <-r.release; return 0, io.EOF }

func TestConcurrentLimit(t *testing.T) {
	s, _ := newTestServer(t, 1)
	br := &blockingReader{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); s.upload(httptest.NewRecorder(), uploadRequest(br, 1, "first.bin")) }()
	select {
	case <-br.entered:
	case <-time.After(time.Second):
		t.Fatal("first upload did not begin")
	}
	rr := httptest.NewRecorder()
	s.upload(rr, uploadRequest(bytes.NewReader([]byte("x")), 1, "second.bin"))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d: %s", rr.Code, rr.Body)
	}
	close(br.release)
	<-done
}

func assertNoUploadArtifacts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == ".tmp" {
			err := filepath.WalkDir(filepath.Join(dir, ".tmp"), func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if path != filepath.Join(dir, ".tmp") {
					t.Fatalf("temporary artifact remains: %s", path)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		t.Fatalf("final artifact remains: %s", entry.Name())
	}
}
