package panel

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Envelope is the unified API response: {"code":0,"message":"ok","data":...}.
type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, env Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(env); err != nil {
		slog.Warn("write json", "err", err)
	}
}

// ok writes a success envelope.
func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, Envelope{Code: CodeOK, Message: "ok", Data: data})
}

// fail writes an error envelope. HTTP status stays 200 for business errors so
// the frontend reads code/message uniformly; auth uses 401 so interceptors can
// react. DB-down uses 503.
func fail(w http.ResponseWriter, err error) {
	ae, isApp := err.(*AppError)
	if !isApp {
		ae = Err(CodeInternal, "服务器内部错误")
		slog.Error("unhandled error", "err", err)
	}
	status := http.StatusOK
	switch ae.Code {
	case CodeUnauthorized, CodeSessionStale:
		status = http.StatusUnauthorized
	case CodeDBDown:
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, Envelope{Code: ae.Code, Message: ae.Msg})
}

// failCode is a shortcut for building+writing an AppError.
func failCode(w http.ResponseWriter, code int, msg string) { fail(w, Err(code, msg)) }
