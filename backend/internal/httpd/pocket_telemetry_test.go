package httpd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type pocketTelemetryGuardStore struct{ controllers.PocketStateService }

func (pocketTelemetryGuardStore) PocketTelemetry(context.Context, string, int64) (domain.PocketTelemetryPage, error) {
	return domain.PocketTelemetryPage{Attempts: []domain.PocketAttemptTelemetry{}}, nil
}

func TestPocketTelemetryLoopbackGuard(t *testing.T) {
	r := chi.NewRouter()
	mountPocketState(r, pocketTelemetryGuardStore{})
	for _, tt := range []struct {
		host, origin string
		status       int
	}{
		{"127.0.0.1:3001", "", http.StatusOK},
		{"192.168.1.2:3001", "", http.StatusForbidden},
		{"127.0.0.1:3001", "https://example.com", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+"/internal/pocket/tasks/task/telemetry", nil)
		if tt.origin != "" {
			req.Header.Set("Origin", tt.origin)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tt.status {
			t.Fatalf("host %s origin %s: status=%d", tt.host, tt.origin, w.Code)
		}
	}
}
