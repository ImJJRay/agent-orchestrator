package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleConversationJSON = `{
	"conversationId":"conv-1",
	"sessionId":"demo-1",
	"harness":"opencode",
	"mode":"chat",
	"controller":"ready",
	"latestSequence":2,
	"oldestSequence":1,
	"hasMoreBefore":false,
	"turns":[{"id":"turn-1","state":"completed","requestedAt":"2026-09-29T12:00:00Z"}],
	"messages":[
		{"id":"msg-1","turnId":"turn-1","sequence":1,"role":"user","origin":"human","text":"read the repo","createdAt":"2026-09-29T12:00:00Z"},
		{"id":"msg-2","turnId":"turn-1","sequence":2,"role":"assistant","origin":"provider","text":"here is what I found","createdAt":"2026-09-29T12:00:05Z"}
	]
}`

func conversationServer(t *testing.T, handler func(*http.Request) (int, string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/conversation") {
			http.NotFound(w, r)
			return
		}
		status, body := handler(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSessionConversation_JSONUsesExistingEndpoint(t *testing.T) {
	cfg := setConfigEnv(t)
	var gotPath, gotLimit string
	srv := conversationServer(t, func(r *http.Request) (int, string) {
		gotPath = r.URL.Path
		gotLimit = r.URL.Query().Get("limit")
		return http.StatusOK, sampleConversationJSON
	})
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "conversation", "demo-1", "--json")
	if err != nil {
		t.Fatalf("conversation failed: %v\nstderr=%s", err, errOut)
	}
	if gotPath != "/api/v1/sessions/demo-1/conversation" || gotLimit != "500" {
		t.Fatalf("request path=%q limit=%q", gotPath, gotLimit)
	}
	for _, want := range []string{`"harness": "opencode"`, `"role": "assistant"`, `"text": "here is what I found"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSessionConversation_PaginatesAndPreservesOldestFirstOrder(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	srv := conversationServer(t, func(r *http.Request) (int, string) {
		requests = append(requests, r.URL.RawQuery)
		if r.URL.Query().Get("beforeSequence") == "" {
			return http.StatusOK, `{
				"conversationId":"conv-1","activeBranchId":"root","sessionId":"demo-1","harness":"opencode",
				"mode":"chat","controller":"ready","latestSequence":4,"oldestSequence":3,"hasMoreBefore":true,
				"turns":[{"id":"turn-2","state":"completed","requestedAt":"2026-09-29T12:01:00Z"}],
				"messages":[
					{"id":"msg-3","turnId":"turn-2","sequence":3,"role":"user","text":"second"},
					{"id":"msg-4","turnId":"turn-2","sequence":4,"role":"assistant","text":"second result"}
				]
			}`
		}
		if r.URL.Query().Get("beforeSequence") != "3" {
			t.Fatalf("beforeSequence=%q, want 3", r.URL.Query().Get("beforeSequence"))
		}
		return http.StatusOK, `{
			"conversationId":"conv-1","activeBranchId":"root","sessionId":"demo-1","harness":"opencode",
			"mode":"chat","controller":"ready","latestSequence":4,"oldestSequence":1,"hasMoreBefore":false,
			"turns":[{"id":"turn-1","state":"completed","requestedAt":"2026-09-29T12:00:00Z"}],
			"messages":[
				{"id":"msg-1","turnId":"turn-1","sequence":1,"role":"user","text":"first"},
				{"id":"msg-2","turnId":"turn-1","sequence":2,"role":"assistant","text":"first result"}
			]
		}`
	})
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "conversation", "demo-1", "--json")
	if err != nil {
		t.Fatalf("conversation failed: %v\nstderr=%s", err, errOut)
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%d, want 2: %#v", len(requests), requests)
	}
	first := strings.Index(out, "first result")
	second := strings.Index(out, "second result")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("messages not reconstructed oldest-first:\n%s", out)
	}
	if !strings.Contains(out, `"hasMoreBefore": false`) {
		t.Fatalf("merged output must be complete:\n%s", out)
	}
}

func TestSessionConversation_FailsClosedIfBranchChangesDuringPagination(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := conversationServer(t, func(r *http.Request) (int, string) {
		if r.URL.Query().Get("beforeSequence") == "" {
			return http.StatusOK, `{"conversationId":"conv-1","activeBranchId":"a","sessionId":"demo-1","mode":"chat","controller":"ready","oldestSequence":2,"hasMoreBefore":true,"turns":[],"messages":[]}`
		}
		return http.StatusOK, `{"conversationId":"conv-1","activeBranchId":"b","sessionId":"demo-1","mode":"chat","controller":"ready","oldestSequence":1,"hasMoreBefore":false,"turns":[],"messages":[]}`
	})
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "conversation", "demo-1", "--json")
	if err == nil || !strings.Contains(err.Error(), "conversation changed") {
		t.Fatalf("err=%v, want paginated consistency failure", err)
	}
}

func TestSessionCommand_HelpDiscoversConversationAndResult(t *testing.T) {
	setConfigEnv(t)
	out, errOut, err := executeCLI(t, Deps{}, "session", "--help")
	if err != nil {
		t.Fatalf("help failed: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{"conversation", "execution", "result"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q:\n%s", want, out)
		}
	}
}
