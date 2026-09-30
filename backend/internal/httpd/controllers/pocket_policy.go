package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

type pocketPolicyService interface {
	ConfigurePocketPolicy(context.Context, string, domain.PocketPolicyConfig) error
	SetPocketDependencies(context.Context, string, []string) error
	PocketPolicy(context.Context, string) (domain.PocketPolicyView, error)
	PocketDecisionHistory(context.Context, string, int64) (domain.PocketDecisionPage, error)
}

func (c *PocketStateController) policyService(w http.ResponseWriter, r *http.Request) (pocketPolicyService, bool) {
	svc, ok := c.Svc.(pocketPolicyService)
	if !ok {
		writePocketError(w, r, errors.New("pocket policy unavailable"))
	}
	return svc, ok
}
func (c *PocketStateController) getPolicy(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.policyService(w, r)
	if !ok {
		return
	}
	v, err := svc.PocketPolicy(r.Context(), chi.URLParam(r, "taskId"))
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, v)
}
func (c *PocketStateController) configurePolicy(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.policyService(w, r)
	if !ok {
		return
	}
	var req domain.PocketPolicyConfig
	if err := decodeJSONStrict(r, &req); err != nil {
		writePocketBadJSON(w, r)
		return
	}
	if err := svc.ConfigurePocketPolicy(r.Context(), chi.URLParam(r, "taskId"), req); err != nil {
		writePocketError(w, r, err)
		return
	}
	c.getPolicy(w, r)
}
func (c *PocketStateController) setDependencies(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.policyService(w, r)
	if !ok {
		return
	}
	var req struct {
		Dependencies []string `json:"dependencies"`
	}
	if err := decodeJSONStrict(r, &req); err != nil {
		writePocketBadJSON(w, r)
		return
	}
	if err := svc.SetPocketDependencies(r.Context(), chi.URLParam(r, "taskId"), req.Dependencies); err != nil {
		writePocketError(w, r, err)
		return
	}
	c.getPolicy(w, r)
}

func (c *PocketStateController) getDecisions(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.policyService(w, r)
	if !ok {
		return
	}
	var before int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			writePocketError(w, r, domain.ErrPocketInvalid)
			return
		}
		before = value
	}
	page, err := svc.PocketDecisionHistory(r.Context(), chi.URLParam(r, "taskId"), before)
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, page)
}
