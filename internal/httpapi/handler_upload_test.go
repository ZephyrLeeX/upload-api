package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
	tmpDir := filepath.Join(dir, "upload-tmp")
	store, err := storage.New(dir, tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{StorageDir: dir, TmpDir: tmpDir, MaxFileSize: 1024, MaxConcurrent: concurrency, UploadToken: testToken}
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
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "测试.bin"))
		if err != nil {
			t.Fatal(err)
		}
		if gotMode := info.Mode().Perm(); gotMode != 0640 {
			t.Fatalf("stored file mode = %04o, want 0640", gotMode)
		}
	}
}

func TestTruncatedHTTPRequestIsSizeMismatch(t *testing.T) {
	dir := t.TempDir()
	tmpDir := filepath.Join(dir, "upload-tmp")
	store, err := storage.New(dir, tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	cfg := config.Config{StorageDir: dir, TmpDir: tmpDir, MaxFileSize: 1024, MaxConcurrent: 1, UploadToken: testToken}
	s := New(cfg, store, slog.New(slog.NewTextHandler(&logs, nil)))
	s.diskFree = func(string) (uint64, error) { return 1 << 40, nil }
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()

	conn, err := net.Dial("tcp", httpServer.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		conn.Close()
		t.Fatal("test connection is not TCP")
	}
	request := "PUT /api/v1/upload/truncated.bin HTTP/1.1\r\n" +
		"Host: " + httpServer.Listener.Addr().String() + "\r\n" +
		"Authorization: Bearer " + testToken + "\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Length: 5\r\n" +
		"Connection: close\r\n\r\nhi"
	if _, err := io.WriteString(tcpConn, request); err != nil {
		tcpConn.Close()
		t.Fatal(err)
	}
	if err := tcpConn.CloseWrite(); err != nil {
		tcpConn.Close()
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(tcpConn), &http.Request{Method: http.MethodPut})
	if err != nil {
		tcpConn.Close()
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want %d", response.StatusCode, http.StatusUnprocessableEntity)
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "size_mismatch" || body.Message != "received size does not match Content-Length" {
		t.Fatalf("unexpected error response: %+v", body)
	}
	if got := logs.String(); !strings.Contains(got, "actual_bytes=2") || !strings.Contains(got, "error_code=size_mismatch") || strings.Contains(got, "error_code=internal_error") {
		t.Fatalf("unexpected request log: %s", got)
	}
	assertNoUploadArtifacts(t, dir)

	rr := httptest.NewRecorder()
	s.upload(rr, uploadRequest(bytes.NewReader([]byte("ok")), 2, "truncated.bin"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("subsequent upload got %d: %s", rr.Code, rr.Body)
	}
}

func TestUploadSizeLimitBoundaries(t *testing.T) {
	t.Run("exact maximum", func(t *testing.T) {
		s, _ := newTestServer(t, 1)
		body := bytes.Repeat([]byte("x"), 1024)
		rr := httptest.NewRecorder()
		s.upload(rr, uploadRequest(bytes.NewReader(body), 1024, "exact.bin"))
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d: %s", rr.Code, rr.Body)
		}
	})

	t.Run("declared maximum plus one", func(t *testing.T) {
		s, dir := newTestServer(t, 1)
		rr := httptest.NewRecorder()
		s.upload(rr, uploadRequest(bytes.NewReader([]byte("x")), 1025, "declared-large.bin"))
		if rr.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("got %d: %s", rr.Code, rr.Body)
		}
		assertNoUploadArtifacts(t, dir)
	})

	t.Run("actual body exceeds maximum", func(t *testing.T) {
		s, dir := newTestServer(t, 1)
		body := bytes.Repeat([]byte("x"), 1025)
		rr := httptest.NewRecorder()
		s.upload(rr, uploadRequest(bytes.NewReader(body), 1024, "actual-large.bin"))
		if rr.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("got %d: %s", rr.Code, rr.Body)
		}
		assertNoUploadArtifacts(t, dir)
	})
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
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got %d: %s", rr.Code, rr.Body)
	}
	var response errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "upload_canceled" {
		t.Fatalf("error = %q, want upload_canceled", response.Error)
	}
	assertNoUploadArtifacts(t, dir)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("test read failure") }

func TestUploadIOErrorRemainsInternalError(t *testing.T) {
	s, dir := newTestServer(t, 1)
	rr := httptest.NewRecorder()
	s.upload(rr, uploadRequest(failingReader{}, 1, "io-error.bin"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("got %d: %s", rr.Code, rr.Body)
	}
	var response errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "internal_error" || response.Message != "internal server error" {
		t.Fatalf("unexpected error response: %+v", response)
	}
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
		if entry.Name() == "upload-tmp" {
			err := filepath.WalkDir(filepath.Join(dir, "upload-tmp"), func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if path != filepath.Join(dir, "upload-tmp") {
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
