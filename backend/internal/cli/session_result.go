package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type sessionResultStatus string

const (
	sessionResultStatusCompleted sessionResultStatus = "completed"
	sessionResultStatusRunning   sessionResultStatus = "running"
	sessionResultStatusFailed    sessionResultStatus = "failed"
	sessionResultStatusMalformed sessionResultStatus = "malformed"
)

// sessionResultOutput is the stable machine-readable contract for
// `ao session result --json`.
type sessionResultOutput struct {
	SessionID    string `json:"sessionId"`
	Status       string `json:"status"`
	TurnID       string `json:"turnId,omitempty"`
	TurnState    string `json:"turnState,omitempty"`
	Result       string `json:"result,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

func (r sessionResultOutput) summary() string {
	switch sessionResultStatus(r.Status) {
	case sessionResultStatusRunning:
		if r.TurnID == "" {
			return "session has not started a turn yet"
		}
		return fmt.Sprintf("turn %s is still %s", r.TurnID, r.TurnState)
	case sessionResultStatusFailed:
		if r.TurnID == "" {
			return r.ErrorMessage
		}
		if r.ErrorMessage != "" {
			return fmt.Sprintf("turn %s ended as %s without a result: %s", r.TurnID, r.TurnState, r.ErrorMessage)
		}
		return fmt.Sprintf("turn %s ended as %s without a result", r.TurnID, r.TurnState)
	case sessionResultStatusMalformed:
		return "conversation state is malformed: " + r.ErrorMessage
	default:
		return ""
	}
}

func newSessionResultCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOptions
	cmd := &cobra.Command{
		Use:   "result <id>",
		Short: "Fetch a session's final completed Chat result",
		Long: "Fetch the exact final assistant message from the most recent relevant non-rolled-back Chat turn. " +
			"The result is derived only from AO's persisted conversation; it is never generated, summarized, or inferred. " +
			"TUI sessions are unsupported until a harness-native semantic result source exists.",
		Example: "  ao session result mer-3\n  ao session result mer-3 --json",
		Args:    oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.sessionResult(cmd.Context(), cmd, id, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func (c *commandContext) sessionResult(ctx context.Context, cmd *cobra.Command, id string, opts sessionOptions) error {
	snapshot, err := c.fetchConversationSnapshot(ctx, id)
	if err != nil {
		return err
	}
	result := deriveSessionResult(id, snapshot)

	if opts.json {
		if err := writeJSON(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	} else if err := writeSessionResultText(cmd, result); err != nil {
		return err
	}

	if sessionResultStatus(result.Status) != sessionResultStatusCompleted {
		return fmt.Errorf("session %s has no completed result: %s", id, result.summary())
	}
	return nil
}

func writeSessionResultText(cmd *cobra.Command, result sessionResultOutput) error {
	out := cmd.OutOrStdout()
	if sessionResultStatus(result.Status) == sessionResultStatusCompleted {
		_, err := fmt.Fprintln(out, result.Result)
		return err
	}
	_, err := fmt.Fprintf(out, "session %s: %s (%s)\n", result.SessionID, result.summary(), result.Status)
	return err
}

// deriveSessionResult is deliberately non-generative. Every returned field is
// copied from AO's persisted turn/message state.
func deriveSessionResult(sessionID string, snapshot conversationSnapshotDTO) sessionResultOutput {
	turn, ok := lastActiveTurn(snapshot.Turns)
	if !ok {
		return sessionResultForNoActiveTurn(sessionID, snapshot.Controller)
	}

	switch turn.State {
	case "queued", "running":
		return sessionResultOutput{
			SessionID: sessionID, Status: string(sessionResultStatusRunning),
			TurnID: turn.ID, TurnState: turn.State,
		}
	case "completed", "recovered":
		msg, ok := lastAssistantMessageForTurn(snapshot.Messages, turn.ID)
		if !ok {
			return sessionResultOutput{
				SessionID: sessionID, Status: string(sessionResultStatusMalformed),
				TurnID: turn.ID, TurnState: turn.State,
				ErrorMessage: fmt.Sprintf("turn %s is %s but the conversation has no assistant message for it", turn.ID, turn.State),
			}
		}
		if msg.Streaming {
			return sessionResultOutput{
				SessionID: sessionID, Status: string(sessionResultStatusMalformed),
				TurnID: turn.ID, TurnState: turn.State,
				ErrorMessage: fmt.Sprintf("turn %s is %s but its assistant message is still streaming", turn.ID, turn.State),
			}
		}
		return sessionResultOutput{
			SessionID: sessionID, Status: string(sessionResultStatusCompleted),
			TurnID: turn.ID, TurnState: turn.State, Result: msg.Text,
		}
	case "failed", "interrupted", "cancelled":
		return sessionResultOutput{
			SessionID: sessionID, Status: string(sessionResultStatusFailed),
			TurnID: turn.ID, TurnState: turn.State, ErrorMessage: turn.ErrorMessage,
		}
	default:
		return sessionResultOutput{
			SessionID: sessionID, Status: string(sessionResultStatusMalformed),
			TurnID: turn.ID, TurnState: turn.State,
			ErrorMessage: fmt.Sprintf("unrecognized turn state %q", turn.State),
		}
	}
}

func sessionResultForNoActiveTurn(sessionID, controller string) sessionResultOutput {
	switch ports.ChatControllerState(controller) {
	case "", ports.ChatControllerConnecting, ports.ChatControllerReady,
		ports.ChatControllerBusy, ports.ChatControllerRecovering:
		return sessionResultOutput{SessionID: sessionID, Status: string(sessionResultStatusRunning)}
	case ports.ChatControllerStopped:
		return sessionResultOutput{
			SessionID: sessionID, Status: string(sessionResultStatusFailed),
			ErrorMessage: "session controller stopped before producing a result",
		}
	default:
		return sessionResultOutput{
			SessionID: sessionID, Status: string(sessionResultStatusMalformed),
			ErrorMessage: fmt.Sprintf("unrecognized controller state %q", controller),
		}
	}
}

func lastActiveTurn(turns []conversationTurnDTO) (conversationTurnDTO, bool) {
	for i := len(turns) - 1; i >= 0; i-- {
		if !turns[i].RolledBack {
			return turns[i], true
		}
	}
	return conversationTurnDTO{}, false
}

func lastAssistantMessageForTurn(messages []conversationMessageDTO, turnID string) (conversationMessageDTO, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].TurnID == turnID && messages[i].Role == "assistant" {
			return messages[i], true
		}
	}
	return conversationMessageDTO{}, false
}
