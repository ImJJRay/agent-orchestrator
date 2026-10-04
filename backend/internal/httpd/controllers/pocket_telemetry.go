package controllers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/pockettelemetry"
)

func (c *PocketStateController) getTelemetry(w http.ResponseWriter, r *http.Request) {
	var after int64
	if raw := r.URL.Query().Get("after"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			writePocketError(w, r, domain.ErrPocketInvalid)
			return
		}
		after = value
	}
	store, ok := c.Svc.(pockettelemetry.Store)
	if !ok {
		writePocketError(w, r, errors.New("pocket telemetry unavailable"))
		return
	}
	page, err := pockettelemetry.Read(r.Context(), store, chi.URLParam(r, "taskId"), after)
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, page)
}
