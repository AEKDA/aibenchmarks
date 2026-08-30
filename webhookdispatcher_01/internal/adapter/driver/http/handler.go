package http

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/google/uuid"
)

// IdempotencyKeyHeader is required on every publish request.
const IdempotencyKeyHeader = "Idempotency-Key"

// maxBodyBytes caps how much of a request body the API will read.
const maxBodyBytes = 1 << 20

// Handlers serves the REST API on top of the driver ports.
type Handlers struct {
	createSubscription ports.CreateSubscription
	publishEvent       ports.PublishEvent
	getDelivery        ports.GetDelivery
	logger             *slog.Logger
}

// NewHandlers builds the adapter.
func NewHandlers(
	createSubscription ports.CreateSubscription,
	publishEvent ports.PublishEvent,
	getDelivery ports.GetDelivery,
	logger *slog.Logger,
) *Handlers {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handlers{
		createSubscription: createSubscription,
		publishEvent:       publishEvent,
		getDelivery:        getDelivery,
		logger:             logger,
	}
}

// NewRouter wires the handlers onto the API routes. Go's ServeMux patterns
// cover the three endpoints, so no third-party router is needed.
func NewRouter(h *Handlers) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/subscriptions", h.CreateSubscription)
	mux.HandleFunc("POST /api/v1/events", h.PublishEvent)
	mux.HandleFunc("GET /api/v1/deliveries/{id}", h.GetDelivery)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

// CreateSubscription handles POST /api/v1/subscriptions.
func (h *Handlers) CreateSubscription(w http.ResponseWriter, r *http.Request) {
	var req createSubscriptionRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, h.logger, err)
		return
	}
	out, err := h.createSubscription.Invoke(r.Context(), ports.CreateSubscriptionInput{
		URL:    req.URL,
		Secret: req.Secret,
		Events: req.Events,
		MaxRPS: req.MaxRPS,
	})
	if err != nil {
		writeError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, newCreateSubscriptionResponse(out))
}

// PublishEvent handles POST /api/v1/events.
func (h *Handlers) PublishEvent(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get(IdempotencyKeyHeader))
	if key == "" {
		writeError(w, h.logger, errs.Invalidf("%s header is required", IdempotencyKeyHeader))
		return
	}
	var req publishEventRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, h.logger, err)
		return
	}
	out, err := h.publishEvent.Invoke(r.Context(), ports.PublishEventInput{
		IdempotencyKey: key,
		Type:           req.Type,
		Payload:        req.Payload,
	})
	if err != nil {
		writeError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, publishEventResponse{
		EventID:      out.EventID,
		Deduplicated: out.Deduplicated,
		Deliveries:   out.DeliveryCount,
	})
}

// GetDelivery handles GET /api/v1/deliveries/{id}.
func (h *Handlers) GetDelivery(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, h.logger, errs.Invalidf("delivery id must be a uuid"))
		return
	}
	delivery, err := h.getDelivery.Invoke(r.Context(), id)
	if err != nil {
		writeError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, newDeliveryResponse(delivery))
}

// decodeBody reads a bounded JSON body into dst.
func decodeBody(r *http.Request, dst any) error {
	body := http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(body)
	if err != nil {
		return errs.Invalidf("request body could not be read")
	}
	if len(raw) == 0 {
		return errs.Invalidf("request body is required")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return errs.Invalidf("request body is not valid json")
	}
	return nil
}
