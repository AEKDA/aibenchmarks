package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/example/webhookdispatcher/internal/application/errs"
)

// internalErrorMessage is what a 500 tells the caller. Storage errors, SQL and
// secrets stay in the log.
const internalErrorMessage = "internal server error"

// writeJSON renders v with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError maps a domain error onto a status code and a safe body.
func writeError(w http.ResponseWriter, logger *slog.Logger, err error) {
	status, message := classify(err)
	if status == http.StatusInternalServerError {
		logger.Error("request failed", slog.String("error", err.Error()))
	}
	writeJSON(w, status, errorResponse{Error: message})
}

// classify returns the status code and the caller-facing message for err.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, errs.ErrNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, errs.ErrConflict), errors.Is(err, errs.ErrAlreadyExists):
		return http.StatusConflict, err.Error()
	case errors.Is(err, errs.ErrInvalidInput):
		return http.StatusBadRequest, err.Error()
	default:
		return http.StatusInternalServerError, internalErrorMessage
	}
}
