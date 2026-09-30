package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestPocketPolicyChatAdmission(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "human task", ClientMessageID: "human", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SendPolicy(ctx, testSession, ports.ChatUserMessage{Text: "policy task", ClientMessageID: "pocket:decision", Origin: domain.MessageOriginAutomation}); !errors.Is(err, chatsvc.ErrTurnRunning) {
		t.Fatalf("busy policy admission: %v", err)
	}
	snap, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Turns) != 1 {
		t.Fatal("policy turn was queued behind human work")
	}
}

func TestPocketPolicyChatIdempotency(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	message := ports.ChatUserMessage{Text: "policy task", ClientMessageID: "pocket:decision", Origin: domain.MessageOriginAutomation}
	first, err := h.svc.SendPolicy(ctx, testSession, message)
	if err != nil {
		t.Fatal(err)
	}
	// AO's ordinary durable send identity must survive a repeated delivery too.
	if _, err := h.svc.Send(ctx, testSession, message); err != nil {
		t.Fatal(err)
	}
	snap, err := h.st.LoadConversationSnapshot(ctx, first.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Turns) != 1 {
		t.Fatalf("duplicate policy delivery created %d turns", len(snap.Turns))
	}
}
