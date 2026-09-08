package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"upload-api/internal/auth"
	"upload-api/internal/storage"
)

const copyBufferSize = 1024 * 1024

func ParseSHA256(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) != sha256.Size*2 {
		return "", errors.New("SHA256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", errors.New("SHA256 must contain only hexadecimal characters")
	}
	return strings.ToLower(value), nil
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	meta, ok := r.Context().Value(requestMetaKey{}).(*requestMeta)
	if !ok {
		meta = &requestMeta{id: "unknown", result: "failed"}
	}
	id := meta.id
	filename := r.PathValue("filename")
	meta.filename = filename
	actual := int64(0)
	computedSHA := ""

	fail := func(httpStatus int, code, message string) {
		meta.result, meta.errorCode = "failed", code
		meta.actualBytes, meta.sha256 = actual, computedSHA
		writeError(w, httpStatus, code, message, id)
	}

	if !auth.ValidateBearer(r.Header.Get("Authorization"), s.cfg.UploadToken) {
		fail(http.StatusUnauthorized, "unauthorized", "missing or invalid Bearer token")
		return
	}
	if err := storage.ValidateFilename(filename); err != nil {
		fail(http.StatusBadRequest, "invalid_filename", "filename is invalid")
		return
	}
	if r.ContentLength < 0 {
		fail(http.StatusLengthRequired, "length_required", "Content-Length is required")
		return
	}
	if r.ContentLength == 0 {
		fail(http.StatusBadRequest, "invalid_size", "Content-Length must be greater than zero")
		return
	}
	if r.ContentLength > s.cfg.MaxFileSize {
		fail(http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds the configured size limit")
		return
	}
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		fail(http.StatusUnsupportedMediaType, "invalid_content_type", "Content-Type must be application/octet-stream")
		return
	}
	expectedSHA, err := ParseSHA256(r.Header.Get("X-File-SHA256"))
	if err != nil {
		fail(http.StatusBadRequest, "invalid_sha256", "X-File-SHA256 is invalid")
		return
	}
	exists, err := s.storage.Exists(filename)
	if err != nil {
		s.logger.Error("check final file failed", "request_id", id, "error", err)
		fail(http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if exists {
		fail(http.StatusConflict, "file_exists", "a file with this name already exists")
		return
	}
	if _, loaded := s.filenames.LoadOrStore(filename, struct{}{}); loaded {
		fail(http.StatusConflict, "file_exists", "a file with this name is already being uploaded")
		return
	}
	defer s.filenames.Delete(filename)
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	default:
		fail(http.StatusTooManyRequests, "too_many_uploads", "too many uploads are in progress")
		return
	}

	available, err := s.diskFree(s.storage.Dir())
	if err != nil {
		s.logger.Error("check available storage failed", "request_id", id, "error", err)
		fail(http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	required := uint64(r.ContentLength)
	if available < s.cfg.MinFreeSpace || required > available-s.cfg.MinFreeSpace {
		fail(http.StatusInsufficientStorage, "insufficient_storage", "insufficient storage space")
		return
	}

	tmp, err := s.storage.CreateTemp(id)
	if err != nil {
		s.logger.Error("create temporary file failed", "request_id", id, "error", err)
		fail(http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	tmpPath := tmp.Name()
	closed := false
	committed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if !committed {
			if err := s.storage.RemoveTemp(tmpPath); err != nil {
				s.logger.Error("remove temporary file failed", "request_id", id, "error", err)
			}
		}
	}()

	hasher := sha256.New()
	limited := http.MaxBytesReader(w, r.Body, s.cfg.MaxFileSize)
	writer := io.MultiWriter(tmp, hasher)
	actual, err = io.CopyBuffer(writer, contextReader{ctx: r.Context(), r: limited}, make([]byte, copyBufferSize))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			fail(http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds the configured size limit")
		} else if r.Context().Err() != nil {
			s.logger.Warn("upload canceled", "request_id", id, "error", r.Context().Err())
			fail(http.StatusBadRequest, "upload_canceled", "upload was canceled")
		} else {
			s.logger.Error("stream upload failed", "request_id", id, "error", err)
			fail(http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	computedSHA = hex.EncodeToString(hasher.Sum(nil))
	if actual != r.ContentLength {
		fail(http.StatusUnprocessableEntity, "size_mismatch", "received size does not match Content-Length")
		return
	}
	if expectedSHA != "" && expectedSHA != computedSHA {
		fail(http.StatusUnprocessableEntity, "sha256_mismatch", "received file SHA256 does not match")
		return
	}
	if err := tmp.Sync(); err != nil {
		s.logger.Error("sync temporary file failed", "request_id", id, "error", err)
		fail(http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if err := tmp.Close(); err != nil {
		closed = true
		s.logger.Error("close temporary file failed", "request_id", id, "error", err)
		fail(http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	closed = true
	if err := s.storage.Commit(tmpPath, filename); err != nil {
		if errors.Is(err, storage.ErrExists) {
			fail(http.StatusConflict, "file_exists", "a file with this name already exists")
		} else {
			s.logger.Error("commit uploaded file failed", "request_id", id, "error", err)
			fail(http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	committed = true
	meta.result, meta.errorCode = "success", ""
	meta.actualBytes, meta.sha256 = actual, computedSHA
	writeJSON(w, http.StatusCreated, uploadResponse{Success: true, Filename: filename, Size: actual, SHA256: computedSHA, RequestID: id})
}
