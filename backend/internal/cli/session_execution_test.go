package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleSessionUsageJSON = `{
	"sessionId":"demo-1",
	"incomplete":false,
	"totals":{
		"inputTokens":100,
		"cachedInputTokens":50,
		"uncachedInputTokens":50,
		"outputTokens":20,
		"processedTokens":120,
		"estimatedCost":{"totalNanos":1234,"inputNanos":800,"cachedInputNanos":100,"outputNanos":334,"coverage":"complete","providerAttribution":"observed"}
	},
	"harnesses":[{"harness":"opencode","totals":{"inputTokens":100,"cachedInputTokens":50,"uncachedInputTokens":50,"outputTokens":20,"processedTokens":120,"estimatedCost":null},"models":[{"modelId":"gpt-5.6-sol","totals":{"inputTokens":100,"cachedInputTokens":50,"uncachedInputTokens":50,"outputTokens":20,"processedTokens":120,"estimatedCost":null}}]}]
}`

func sessionExecutionServer(t *testing.T, conversation func(*http.Request) (int, string), usageBody string, mode string) *httptest.Server {
	t.Helper()
	if mode == "" {
		mode = "chat"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			body := sessionJSON("demo-1", "demo", "worker", "idle", false)
			if mode != "chat" {
				body = strings.Replace(body, `"mode":"chat"`, `"mode":"`+mode+`"`, 1)
			}
			_, _ = io.WriteString(w, `{"session":`+body+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1/conversation":
			status, body := conversation(r)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/usage/sessions/demo-1":
			_, _ = io.WriteString(w, usageBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSessionExecution_NormalizesDurableEvidenceWithoutInventingTurnUsage(t *testing.T) {
	cfg := setConfigEnv(t)
	conversation := `{
		"conversationId":"conv-1","activeBranchId":"root","sessionId":"demo-1","harness":"opencode","mode":"chat","controller":"ready",
		"latestSequence":3,"oldestSequence":1,"hasMoreBefore":false,
		"turns":[{"id":"turn-1","state":"completed","requestedAt":"2026-09-30T00:00:00Z","startedAt":"2026-09-30T00:00:01Z","completedAt":"2026-09-30T00:00:06Z","diff":{"files":[{"path":"x.go","status":"modified","additions":3,"deletions":1}]}}],
		"messages":[{"id":"m1","turnId":"turn-1","sequence":1,"role":"assistant","text":"done","streaming":false}],
		"activities":[]
	}`
	srv := sessionExecutionServer(t, func(*http.Request) (int, string) { return http.StatusOK, conversation }, sampleSessionUsageJSON, "chat")
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "execution", "demo-1", "--json")
	if err != nil {
		t.Fatalf("execution failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionExecutionOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out)
	}
	if got.SchemaVersion != pocketExecutionSchemaVersion || got.Execution == nil || got.Execution.ID != "turn-1" {
		t.Fatalf("unexpected execution identity: %#v", got)
	}
	if got.Execution.DurationMillis == nil || *got.Execution.DurationMillis != 5000 {
		t.Fatalf("duration=%v, want 5000ms", got.Execution.DurationMillis)
	}
	if !got.Execution.ChangedFilesKnown || len(got.Execution.ChangedFiles) != 1 || got.Execution.ChangedFiles[0].Path != "x.go" {
		t.Fatalf("changed files=%#v", got.Execution)
	}
	if got.Result.Status != "completed" || got.Result.Result != "done" {
		t.Fatalf("result=%#v", got.Result)
	}
	if got.Usage.Scope != "session" || got.Usage.ExecutionScoped {
		t.Fatalf("usage scope=%#v", got.Usage)
	}
	if got.Usage.CacheHitRatio == nil || *got.Usage.CacheHitRatio != 0.5 {
		t.Fatalf("cache ratio=%v, want 0.5", got.Usage.CacheHitRatio)
	}
	if got.Policy.State != "awaiting_evidence" || got.Policy.AutomaticAcceptanceAllowed || got.Policy.MergeAuthorized || got.Policy.SemanticReviewEligible {
		t.Fatalf("policy=%#v", got.Policy)
	}
	for _, want := range []string{"executionUsage", "executionCost", "profile.effort", "validationResults", "dependencyState"} {
		if !containsString(got.Unknown, want) {
			t.Fatalf("unknown missing %q: %#v", want, got.Unknown)
		}
	}
}

func TestSessionExecution_PendingInteractionFromEarlierPageBlocksPolicy(t *testing.T) {
	cfg := setConfigEnv(t)
	conversation := func(r *http.Request) (int, string) {
		if r.URL.Query().Get("beforeSequence") == "" {
			return http.StatusOK, `{
				"conversationId":"conv-1","activeBranchId":"root","sessionId":"demo-1","harness":"opencode","mode":"chat","controller":"ready",
				"latestSequence":4,"oldestSequence":3,"hasMoreBefore":true,
				"turns":[{"id":"turn-1","state":"completed","requestedAt":"2026-09-30T00:00:00Z"}],
				"messages":[{"id":"m1","turnId":"turn-1","sequence":4,"role":"assistant","text":"done"}],"activities":[]
			}`
		}
		return http.StatusOK, `{
			"conversationId":"conv-1","activeBranchId":"root","sessionId":"demo-1","harness":"opencode","mode":"chat","controller":"ready",
			"latestSequence":4,"oldestSequence":1,"hasMoreBefore":false,"turns":[],"messages":[],
			"activities":[{"id":"a1","turnId":"turn-1","sequence":2,"activityKind":"approval","status":"pending","requestId":"approve-1"}]
		}`
	}
	srv := sessionExecutionServer(t, conversation, sampleSessionUsageJSON, "chat")
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "execution", "demo-1", "--json")
	if err != nil {
		t.Fatalf("execution failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionExecutionOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Policy.State != "blocked" {
		t.Fatalf("policy state=%q, want blocked", got.Policy.State)
	}
	if gateState(got.Policy.Gates, "blocking_interaction") != "fail" {
		t.Fatalf("gates=%#v", got.Policy.Gates)
	}
}

func TestSessionExecution_UnknownUsageStaysUnknown(t *testing.T) {
	cfg := setConfigEnv(t)
	conversation := `{"conversationId":"c","sessionId":"demo-1","mode":"chat","controller":"ready","turns":[{"id":"t","state":"completed","requestedAt":"2026-09-30T00:00:00Z"}],"messages":[{"id":"m","turnId":"t","role":"assistant","text":"done"}],"activities":[]}`
	usage := `{"sessionId":"demo-1","incomplete":true,"totals":{"inputTokens":null,"cachedInputTokens":null,"uncachedInputTokens":null,"outputTokens":null,"processedTokens":null,"estimatedCost":null},"harnesses":[]}`
	srv := sessionExecutionServer(t, func(*http.Request) (int, string) { return http.StatusOK, conversation }, usage, "chat")
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "execution", "demo-1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got sessionExecutionOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Usage.CacheHitRatio != nil || got.Usage.Totals.InputTokens != nil || got.Usage.Totals.EstimatedCost != nil {
		t.Fatalf("unknown usage was invented: %#v", got.Usage)
	}
	for _, want := range []string{"usage.inputTokens", "usage.cachedInputTokens", "usage.uncachedInputTokens", "usage.outputTokens", "usage.estimatedCost"} {
		if !containsString(got.Unknown, want) {
			t.Fatalf("unknown missing %q: %#v", want, got.Unknown)
		}
	}
}

func TestAssessCompletionPolicy_RunningAndFailedResultsBlock(t *testing.T) {
	for _, status := range []string{"running", "failed", "malformed"} {
		t.Run(status, func(t *testing.T) {
			got := assessCompletionPolicy(sessionResultOutput{Status: status}, conversationSnapshotDTO{})
			if got.State != "blocked" || gateState(got.Gates, "semantic_result") != "fail" || got.MergeAuthorized || got.AutomaticAcceptanceAllowed {
				t.Fatalf("policy=%#v", got)
			}
		})
	}
}

func TestSessionExecution_TUIModeFailsClearly(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := sessionExecutionServer(t, func(*http.Request) (int, string) { return http.StatusInternalServerError, `{}` }, sampleSessionUsageJSON, "tui")
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "execution", "demo-1", "--json")
	if err == nil || !strings.Contains(err.Error(), "requires durable Chat semantics") {
		t.Fatalf("err=%v", err)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func gateState(gates []completionGateOutput, name string) string {
	for _, gate := range gates {
		if gate.Name == name {
			return gate.State
		}
	}
	return ""
}
