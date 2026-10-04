package chat_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestPocketTelemetryArchivesNativeTurnIdentity(t *testing.T) {
	conv := newFakeConversation()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()
	turn, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "attempt"})
	if err != nil {
		t.Fatal(err)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID, NativeTurnID: "native-attempt-turn"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	events, err := h.st.ProviderEventsSince(ctx, h.ctrl.ConversationID(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJson), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["nativeTurnId"] == "native-attempt-turn" && payload["providerTurnId"] == turn.ProviderTurnID {
			return
		}
	}
	t.Fatal("native and scoped turn identities were not archived together")
}
