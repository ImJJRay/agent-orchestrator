package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type telemetryFake struct {
	controllers.PocketStateService
	err   error
	after int64
}

func (f *telemetryFake) PocketTelemetry(_ context.Context, task string, after int64) (domain.PocketTelemetryPage, error) {
	f.after = after
	return domain.PocketTelemetryPage{TaskID: task, Attempts: []domain.PocketAttemptTelemetry{{Execution: domain.PocketExecution{ID: "attempt"}}}}, f.err
}

func TestPocketTelemetryAPI(t *testing.T) {
	f := &telemetryFake{}
	r := chi.NewRouter()
	(&controllers.PocketStateController{Svc: f}).Register(r)
	for _, tt := range []struct {
		query  string
		err    error
		status int
	}{
		{"", nil, 200}, {"?after=7", nil, 200}, {"?after=-1", nil, 400}, {"?after=no", nil, 400}, {"?after=999999999999999999999", nil, 400},
		{"", domain.ErrPocketNotFound, 404}, {"", errors.New("db failure"), 500},
	} {
		f.err = tt.err
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/pocket/tasks/task/telemetry"+tt.query, nil))
		if w.Code != tt.status {
			t.Fatalf("%s: %d %s", tt.query, w.Code, w.Body)
		}
		if tt.status == 200 {
			var page domain.PocketTelemetryPage
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.TaskID != "task" || page.Attempts[0].Coverage != "unavailable" || page.Attempts[0].InputTokens != nil {
				t.Fatalf("unknown usage: %+v", page)
			}
			if tt.query != "" && f.after != 7 {
				t.Fatalf("cursor: %d", f.after)
			}
		}
	}
}
