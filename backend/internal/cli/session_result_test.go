package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestDeriveSessionResult_CompletedReturnsExactLatestAssistantText(t *testing.T) {
	snapshot := conversationSnapshotDTO{
		Turns: []conversationTurnDTO{{ID: "turn-1", State: "completed"}},
		Messages: []conversationMessageDTO{
			{ID: "m1", TurnID: "turn-1", Role: "assistant", Text: "intermediate"},
			{ID: "m2", TurnID: "turn-1", Role: "assistant", Text: "exact final result"},
		},
	}
	got := deriveSessionResult("demo-1", snapshot)
	if got.Status != "completed" || got.Result != "exact final result" || got.TurnID != "turn-1" {
		t.Fatalf("result=%#v", got)
	}
}

func TestDeriveSessionResult_SkipsRolledBackTurn(t *testing.T) {
	snapshot := conversationSnapshotDTO{
		Turns: []conversationTurnDTO{
			{ID: "turn-1", State: "completed"},
			{ID: "turn-2", State: "completed", RolledBack: true},
		},
		Messages: []conversationMessageDTO{{ID: "m1", TurnID: "turn-1", Role: "assistant", Text: "kept"}},
	}
	got := deriveSessionResult("demo-1", snapshot)
	if got.Status != "completed" || got.Result != "kept" || got.TurnID != "turn-1" {
		t.Fatalf("result=%#v", got)
	}
}

func TestDeriveSessionResult_StateClassification(t *testing.T) {
	tests := []struct {
		name       string
		snapshot   conversationSnapshotDTO
		wantStatus string
	}{
		{"queued", conversationSnapshotDTO{Turns: []conversationTurnDTO{{ID: "t", State: "queued"}}}, "running"},
		{"running", conversationSnapshotDTO{Turns: []conversationTurnDTO{{ID: "t", State: "running"}}}, "running"},
		{"failed", conversationSnapshotDTO{Turns: []conversationTurnDTO{{ID: "t", State: "failed", ErrorMessage: "boom"}}}, "failed"},
		{"interrupted", conversationSnapshotDTO{Turns: []conversationTurnDTO{{ID: "t", State: "interrupted"}}}, "failed"},
		{"cancelled", conversationSnapshotDTO{Turns: []conversationTurnDTO{{ID: "t", State: "cancelled"}}}, "failed"},
		{"completed-without-assistant", conversationSnapshotDTO{Turns: []conversationTurnDTO{{ID: "t", State: "completed"}}}, "malformed"},
		{"completed-streaming", conversationSnapshotDTO{
			Turns:    []conversationTurnDTO{{ID: "t", State: "completed"}},
			Messages: []conversationMessageDTO{{ID: "m", TurnID: "t", Role: "assistant", Text: "partial", Streaming: true}},
		}, "malformed"},
		{"unknown-turn", conversationSnapshotDTO{Turns: []conversationTurnDTO{{ID: "t", State: "future-state"}}}, "malformed"},
		{"no-turn-live-controller", conversationSnapshotDTO{Controller: "ready"}, "running"},
		{"no-turn-stopped-controller", conversationSnapshotDTO{Controller: "stopped"}, "failed"},
		{"unknown-controller", conversationSnapshotDTO{Controller: "future-controller"}, "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveSessionResult("demo-1", tt.snapshot)
			if got.Status != tt.wantStatus {
				t.Fatalf("status=%q, want %q; result=%#v", got.Status, tt.wantStatus, got)
			}
			if got.Status != "completed" && got.Result != "" {
				t.Fatalf("non-completed state fabricated result: %#v", got)
			}
		})
	}
}

func TestSessionResult_CompletedJSONExitsZero(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := conversationServer(t, func(*http.Request) (int, string) {
		return http.StatusOK, sampleConversationJSON
	})
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "result", "demo-1", "--json")
	if err != nil {
		t.Fatalf("result failed: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{`"status": "completed"`, `"result": "here is what I found"`, `"turnId": "turn-1"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSessionResult_NonCompletedJSONExitsNonZero(t *testing.T) {
	for _, tc := range []struct {
		name, body, status string
	}{
		{"running", `{"conversationId":"c","sessionId":"demo-1","mode":"chat","controller":"busy","turns":[{"id":"t","state":"running"}],"messages":[]}`, "running"},
		{"failed", `{"conversationId":"c","sessionId":"demo-1","mode":"chat","controller":"stopped","turns":[{"id":"t","state":"failed","errorMessage":"provider crashed"}],"messages":[]}`, "failed"},
		{"malformed", `{"conversationId":"c","sessionId":"demo-1","mode":"chat","controller":"ready","turns":[{"id":"t","state":"completed"}],"messages":[]}`, "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv := conversationServer(t, func(*http.Request) (int, string) {
				return http.StatusOK, tc.body
			})
			writeRunFileFor(t, cfg, srv)

			out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
				"session", "result", "demo-1", "--json")
			if err == nil || ExitCode(err) != 1 {
				t.Fatalf("err=%v exit=%d, want runtime failure", err, ExitCode(err))
			}
			if !strings.Contains(out, `"status": "`+tc.status+`"`) {
				t.Fatalf("output missing status %q:\n%s", tc.status, out)
			}
			if strings.Contains(out, `"result"`) {
				t.Fatalf("non-completed state must not contain result:\n%s", out)
			}
		})
	}
}

func TestSessionResult_UsesAllConversationPages(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := conversationServer(t, func(r *http.Request) (int, string) {
		if r.URL.Query().Get("beforeSequence") == "" {
			return http.StatusOK, `{
				"conversationId":"c","activeBranchId":"root","sessionId":"demo-1","mode":"chat","controller":"ready",
				"oldestSequence":3,"hasMoreBefore":true,
				"turns":[{"id":"rolled","state":"completed","rolledBack":true}],
				"messages":[]
			}`
		}
		return http.StatusOK, `{
			"conversationId":"c","activeBranchId":"root","sessionId":"demo-1","mode":"chat","controller":"ready",
			"oldestSequence":1,"hasMoreBefore":false,
			"turns":[{"id":"kept","state":"completed"}],
			"messages":[{"id":"m","turnId":"kept","sequence":2,"role":"assistant","text":"older kept result"}]
		}`
	})
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "result", "demo-1", "--json")
	if err != nil {
		t.Fatalf("result failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, `"result": "older kept result"`) {
		t.Fatalf("result did not traverse pagination:\n%s", out)
	}
}
