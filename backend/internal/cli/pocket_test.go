package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPocketCLI(t *testing.T) {
	cfg := setConfigEnv(t)
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/pocket/tasks/task-id/policy" && r.URL.Path != "/internal/pocket/tasks/task-id/decisions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = io.WriteString(w, `{"message":"task missing","code":"POCKET_NOT_FOUND","requestId":"req"}`)
			return
		}
		_, _ = io.WriteString(w, `{"policy":{"state":"RETRY","reason":"bounded_action_permitted","allowed":true},"facts":{"attempts":1,"retries":0,"escalations":0,"config":{"maxAttempts":3,"maxRetries":1,"maxEscalations":1}},"decisions":[{"id":"audit-1","outcome":{"state":"RETRY","allowed":true,"reason":"bounded_action_permitted"}}],"actions":[]}`)
	}))
	defer server.Close()
	writeRunFileFor(t, cfg, server)
	for _, args := range [][]string{{"pocket", "policy", "task-id"}, {"pocket", "policy", "task-id", "--json"}, {"pocket", "decisions", "task-id"}, {"pocket", "decisions", "task-id", "--json"}} {
		var out strings.Builder
		deps := Deps{Out: &out, ProcessAlive: func(int) bool { return true }}
		cmd := NewRootCommand(deps)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "RETRY") {
			t.Fatalf("missing policy output: %s", out.String())
		}
	}
	status = http.StatusNotFound
	var out strings.Builder
	cmd := NewRootCommand(Deps{Out: &out, ProcessAlive: func(int) bool { return true }})
	cmd.SetArgs([]string{"pocket", "policy", "task-id"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "POCKET_NOT_FOUND") {
		t.Fatalf("daemon error: %v", err)
	}
	cmd = NewRootCommand(Deps{Out: &out, ProcessAlive: func(int) bool { return true }})
	cmd.SetArgs([]string{"pocket", "policy"})
	if err := cmd.Execute(); ExitCode(err) != 2 {
		t.Fatalf("usage exit: %v", err)
	}
}
