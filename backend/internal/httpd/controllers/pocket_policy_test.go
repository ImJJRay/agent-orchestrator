package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type policyServiceFake struct {
	controllers.PocketStateService
	config       domain.PocketPolicyConfig
	dependencies []string
	err          error
}

func (f *policyServiceFake) ConfigurePocketPolicy(_ context.Context, _ string, c domain.PocketPolicyConfig) error {
	f.config = c
	return f.err
}
func (f *policyServiceFake) SetPocketDependencies(_ context.Context, _ string, deps []string) error {
	f.dependencies = deps
	return f.err
}
func (f *policyServiceFake) PocketPolicy(context.Context, string) (domain.PocketPolicyView, error) {
	return domain.PocketPolicyView{Policy: domain.PocketPolicyOutcome{State: "BLOCKED", Reason: "dependencies_not_satisfied"}, Decisions: []domain.PocketDecision{{ID: "audit", Outcome: domain.PocketPolicyOutcome{State: "BLOCKED"}}}}, f.err
}

func TestPocketPolicyAPI(t *testing.T) {
	f := &policyServiceFake{}
	router := chi.NewRouter()
	(&controllers.PocketStateController{Svc: f}).Register(router)
	tests := []struct {
		method, path, body string
		err                error
		status             int
	}{
		{http.MethodGet, "policy", "", nil, 200},
		{http.MethodPut, "policy", `{"auto":true,"sessionId":"worker","maxAttempts":3,"maxRetries":1,"maxEscalations":1,"escalationSessions":["stronger"]}`, nil, 200},
		{http.MethodPut, "dependencies", `{"dependencies":["prior"]}`, nil, 200},
		{http.MethodPut, "policy", `{"unexpected":true}`, nil, 400},
		{http.MethodPut, "dependencies", `{`, nil, 400},
		{http.MethodGet, "policy", "", domain.ErrPocketNotFound, 404},
		{http.MethodPut, "dependencies", `{"dependencies":["self"]}`, domain.ErrPocketInvalid, 400},
		{http.MethodPut, "dependencies", `{"dependencies":["late"]}`, domain.ErrPocketConflict, 409},
		{http.MethodGet, "policy", "", errors.New("storage unavailable"), 500},
	}
	for _, tt := range tests {
		t.Run(tt.method+tt.path+http.StatusText(tt.status), func(t *testing.T) {
			f.err = tt.err
			r := httptest.NewRequest(tt.method, "/internal/pocket/tasks/task/"+tt.path, strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if tt.status == 200 {
				var v domain.PocketPolicyView
				if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
					t.Fatal(err)
				}
				if v.Policy.Reason != "dependencies_not_satisfied" || v.Decisions[0].ID != "audit" {
					t.Fatalf("visibility: %#v", v)
				}
			}
		})
	}
	if f.config.SessionID != "worker" || len(f.config.EscalationSessions) != 1 {
		t.Fatalf("config not decoded: %#v", f.config)
	}
}

func (f *policyServiceFake) PocketDecisionHistory(context.Context, string, int64) (domain.PocketDecisionPage, error) {
	return domain.PocketDecisionPage{Decisions: []domain.PocketDecision{{ID: "audit", Outcome: domain.PocketPolicyOutcome{State: "BLOCKED"}}}}, f.err
}

func TestPocketPolicyHistoryAPI(t *testing.T) {
	router := chi.NewRouter()
	(&controllers.PocketStateController{Svc: &policyServiceFake{}}).Register(router)
	for _, tt := range []struct {
		query  string
		status int
	}{{"", 200}, {"?before=4", 200}, {"?before=-1", 400}, {"?before=bad", 400}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/pocket/tasks/task/decisions"+tt.query, nil))
		if w.Code != tt.status {
			t.Fatalf("history status=%d", w.Code)
		}
	}
}
