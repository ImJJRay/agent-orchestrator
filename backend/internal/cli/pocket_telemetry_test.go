package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPocketTelemetryCLI(t *testing.T) {
	cfg := setConfigEnv(t)
	status := http.StatusOK
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/telemetry/cli-invoked" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/internal/pocket/tasks/task/telemetry" || r.URL.Query().Get("after") != "2" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != 200 {
			_, _ = io.WriteString(w, `{"code":"POCKET_NOT_FOUND","message":"task missing","requestId":"req-telemetry"}`)
			return
		}
		_, _ = io.WriteString(w, `{"taskId":"task","attempts":[{"execution":{"id":"attempt","attemptNumber":3,"state":"failed"},"coverage":"unavailable","reason":"no_exact_native_turn_usage","costCoverage":"unknown"}],"nextAfter":3}`)
	}))
	defer server.Close()
	writeRunFileFor(t, cfg, server)
	for _, jsonMode := range []bool{false, true} {
		var out strings.Builder
		cmd := NewRootCommand(Deps{Out: &out, ProcessAlive: func(int) bool { return true }})
		args := []string{"pocket", "telemetry", "task", "--after", "2"}
		if jsonMode {
			args = append(args, "--json")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "unavailable") {
			t.Fatalf("output: %s", out.String())
		}
		if !jsonMode && (!strings.Contains(out.String(), "Input=unknown") || !strings.Contains(out.String(), "--after 3")) {
			t.Fatalf("human output: %s", out.String())
		}
	}
	status = http.StatusNotFound
	cmd := NewRootCommand(Deps{ProcessAlive: func(int) bool { return true }})
	cmd.SetArgs([]string{"pocket", "telemetry", "task", "--after", "2"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "POCKET_NOT_FOUND") || !strings.Contains(err.Error(), "req-telemetry") {
		t.Fatalf("error envelope: %v", err)
	}
	before := calls
	for _, args := range [][]string{{"pocket", "telemetry"}, {"pocket", "telemetry", "task", "extra"}, {"pocket", "telemetry", "task", "--after", "-1"}} {
		cmd := NewRootCommand(Deps{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); ExitCode(err) != 2 {
			t.Fatalf("usage error: %v", err)
		}
	}
	if calls != before {
		t.Fatal("invalid arguments reached daemon")
	}
}
