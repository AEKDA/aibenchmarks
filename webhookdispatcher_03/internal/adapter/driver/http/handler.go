// Package http — inbound REST-адаптер (driver): принимает HTTP-запросы извне
// и диспетчеризует их в usecase'ы приложения.
package http

import (
	"encoding/json"
	nethttp "net/http"
	"time"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/errs"
	"webhookdispatcher/internal/application/usecase"
)

// handler набор usecase'ов, обслуживающих входящие REST-запросы.
type handler struct {
	createSubscription *usecase.CreateSubscription
	publishEvent       *usecase.PublishEvent
	getDelivery        *usecase.GetDelivery
}

// NewHandler собирает HTTP-адаптер с маршрутами:
//
//	POST /api/v1/subscriptions   — регистрация подписчика
//	POST /api/v1/events          — публикация события (обязателен Idempotency-Key)
//	GET  /api/v1/deliveries/{id} — статус доставки
func NewHandler(cs *usecase.CreateSubscription, pe *usecase.PublishEvent, gd *usecase.GetDelivery) nethttp.Handler {
	h := &handler{createSubscription: cs, publishEvent: pe, getDelivery: gd}
	mux := nethttp.NewServeMux()
	mux.HandleFunc("POST /api/v1/subscriptions", h.handleCreateSubscription)
	mux.HandleFunc("POST /api/v1/events", h.handlePublishEvent)
	mux.HandleFunc("GET /api/v1/deliveries/{id}", h.handleGetDelivery)
	return mux
}

// handleCreateSubscription регистрирует нового подписчика.
func (h *handler) handleCreateSubscription(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req SubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		nethttp.Error(w, "invalid json body", nethttp.StatusBadRequest)
		return
	}
	sub, err := h.createSubscription.Invoke(r.Context(), usecase.CreateSubscriptionIn{
		URL: req.URL, Secret: req.Secret, Events: req.Events, MaxRPS: req.MaxRPS,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusCreated, SubscriptionResponse{ID: sub.ID, Status: "active"})
}

// handlePublishEvent публикует событие; требует заголовок Idempotency-Key.
func (h *handler) handlePublishEvent(w nethttp.ResponseWriter, r *nethttp.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		nethttp.Error(w, "missing Idempotency-Key header", nethttp.StatusBadRequest)
		return
	}
	var req EventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		nethttp.Error(w, "invalid json body", nethttp.StatusBadRequest)
		return
	}
	out, err := h.publishEvent.Invoke(r.Context(), usecase.PublishEventIn{
		Type: req.Type, Payload: req.Payload, IdempotencyKey: key, Now: time.Now().UTC(),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, PublishResponse{ID: out.EventID, Duplicate: out.Duplicate})
}

// handleGetDelivery возвращает статус доставки по ID.
func (h *handler) handleGetDelivery(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, errs.ErrInvalid)
		return
	}
	d, err := h.getDelivery.Invoke(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, deliveryToResponse(d))
}

// writeJSON пишет v в JSON с нужным статусом.
func writeJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
