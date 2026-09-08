package httpapi

import (
	"encoding/json"
	"net/http"
)

type errorResponse struct {
	Success   bool   `json:"success"`
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type uploadResponse struct {
	Success   bool   `json:"success"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	RequestID string `json:"request_id"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message, requestID string) {
	writeJSON(w, status, errorResponse{Success: false, Error: code, Message: message, RequestID: requestID})
}
