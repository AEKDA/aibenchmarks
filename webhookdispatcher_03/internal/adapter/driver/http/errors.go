package http

import (
	nethttp "net/http"

	"webhookdispatcher/internal/application/errs"
)

// writeError маппит доменную ошибку в HTTP-статус и тело ответа.
func writeError(w nethttp.ResponseWriter, err error) {
	switch {
	case errs.Is(err, errs.ErrNotFound):
		nethttp.Error(w, "not found", nethttp.StatusNotFound)
	case errs.Is(err, errs.ErrConflict):
		nethttp.Error(w, "conflict", nethttp.StatusConflict)
	case errs.Is(err, errs.ErrInvalid):
		nethttp.Error(w, "invalid", nethttp.StatusBadRequest)
	default:
		nethttp.Error(w, "internal", nethttp.StatusInternalServerError)
	}
}
