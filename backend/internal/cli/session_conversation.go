package cli

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

// conversationSnapshotDTO mirrors the semantic subset of the daemon's
// ConversationSnapshotResponse used by the CLI. Production snapshots are
// paginated, so fetchConversationSnapshot reconstructs the complete active
// conversation before returning this value.
type conversationSnapshotDTO struct {
	ConversationID string                    `json:"conversationId"`
	ActiveBranchID string                    `json:"activeBranchId,omitempty"`
	SessionID      string                    `json:"sessionId"`
	Harness        string                    `json:"harness,omitempty"`
	Mode           string                    `json:"mode"`
	Controller     string                    `json:"controller"`
	Title          string                    `json:"title,omitempty"`
	LatestSequence int64                     `json:"latestSequence"`
	OldestSequence int64                     `json:"oldestSequence,omitempty"`
	HasMoreBefore  bool                      `json:"hasMoreBefore"`
	Turns          []conversationTurnDTO     `json:"turns"`
	Messages       []conversationMessageDTO  `json:"messages"`
	Activities     []conversationActivityDTO `json:"activities"`
}

type conversationTurnDTO struct {
	ID              string                   `json:"id"`
	State           string                   `json:"state"`
	RetryOfTurnID   string                   `json:"retryOfTurnId,omitempty"`
	HasRetryAttempt bool                     `json:"hasRetryAttempt,omitempty"`
	ErrorMessage    string                   `json:"errorMessage,omitempty"`
	RequestedAt     string                   `json:"requestedAt"`
	StartedAt       *string                  `json:"startedAt,omitempty"`
	CompletedAt     *string                  `json:"completedAt,omitempty"`
	RolledBack      bool                     `json:"rolledBack,omitempty"`
	Diff            *conversationTurnDiffDTO `json:"diff,omitempty"`
}

type conversationTurnDiffDTO struct {
	Files     []conversationDiffFileDTO `json:"files"`
	Truncated bool                      `json:"truncated,omitempty"`
}

type conversationDiffFileDTO struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type conversationActivityDTO struct {
	ID           string `json:"id"`
	TurnID       string `json:"turnId,omitempty"`
	Sequence     int64  `json:"sequence"`
	ActivityKind string `json:"activityKind"`
	Status       string `json:"status"`
	RequestID    string `json:"requestId,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
}

type conversationMessageDTO struct {
	ID        string `json:"id"`
	TurnID    string `json:"turnId,omitempty"`
	Sequence  int64  `json:"sequence"`
	Role      string `json:"role"`
	Origin    string `json:"origin"`
	Text      string `json:"text"`
	Streaming bool   `json:"streaming"`
	CreatedAt string `json:"createdAt"`
}

func newSessionConversationCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOptions
	cmd := &cobra.Command{
		Use:   "conversation <id>",
		Short: "Show a session's stored Chat conversation",
		Long: "Show the complete active-branch Chat conversation persisted by AO. " +
			"The CLI follows the daemon's paginated conversation API; it does not scrape terminal output. " +
			"TUI sessions do not have this semantic conversation contract.",
		Args: oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.getSessionConversation(cmd.Context(), cmd, id, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func (c *commandContext) getSessionConversation(ctx context.Context, cmd *cobra.Command, id string, opts sessionOptions) error {
	snapshot, err := c.fetchConversationSnapshot(ctx, id)
	if err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), snapshot)
	}
	return writeConversationTranscript(cmd, snapshot)
}

func (c *commandContext) fetchConversationSnapshot(ctx context.Context, id string) (conversationSnapshotDTO, error) {
	const pageLimit = 500

	var pages []conversationSnapshotDTO
	var before int64
	for {
		params := url.Values{}
		params.Set("limit", strconv.Itoa(pageLimit))
		if before > 0 {
			params.Set("beforeSequence", strconv.FormatInt(before, 10))
		}

		var page conversationSnapshotDTO
		path := apiPath("sessions/"+url.PathEscape(id)+"/conversation", params)
		if err := c.getJSON(ctx, path, &page); err != nil {
			return conversationSnapshotDTO{}, err
		}
		if len(pages) > 0 {
			first := pages[0]
			if page.SessionID != first.SessionID || page.ConversationID != first.ConversationID || page.ActiveBranchID != first.ActiveBranchID {
				return conversationSnapshotDTO{}, fmt.Errorf("conversation changed while reading paginated snapshot")
			}
		}
		pages = append(pages, page)
		if !page.HasMoreBefore {
			break
		}
		next := page.OldestSequence
		if next <= 0 || (before > 0 && next >= before) {
			return conversationSnapshotDTO{}, fmt.Errorf("conversation pagination did not make progress")
		}
		before = next
	}

	return mergeConversationPages(pages), nil
}

func mergeConversationPages(pages []conversationSnapshotDTO) conversationSnapshotDTO {
	if len(pages) == 0 {
		return conversationSnapshotDTO{}
	}
	out := pages[0]
	out.Turns = nil
	out.Messages = nil
	out.Activities = nil
	out.HasMoreBefore = false

	seenTurns := make(map[string]struct{})
	seenMessages := make(map[string]struct{})
	seenActivities := make(map[string]struct{})
	for i := len(pages) - 1; i >= 0; i-- {
		page := pages[i]
		out.OldestSequence = page.OldestSequence
		for _, turn := range page.Turns {
			if _, ok := seenTurns[turn.ID]; ok {
				continue
			}
			seenTurns[turn.ID] = struct{}{}
			out.Turns = append(out.Turns, turn)
		}
		for _, msg := range page.Messages {
			if _, ok := seenMessages[msg.ID]; ok {
				continue
			}
			seenMessages[msg.ID] = struct{}{}
			out.Messages = append(out.Messages, msg)
		}
		for _, activity := range page.Activities {
			if _, ok := seenActivities[activity.ID]; ok {
				continue
			}
			seenActivities[activity.ID] = struct{}{}
			out.Activities = append(out.Activities, activity)
		}
	}
	return out
}

func writeConversationTranscript(cmd *cobra.Command, snapshot conversationSnapshotDTO) error {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "session: %s\n", snapshot.SessionID); err != nil {
		return err
	}
	if snapshot.Harness != "" {
		if _, err := fmt.Fprintf(out, "harness: %s\n", snapshot.Harness); err != nil {
			return err
		}
	}
	if snapshot.Title != "" {
		if _, err := fmt.Fprintf(out, "title: %s\n", snapshot.Title); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "controller: %s\n\n", snapshot.Controller); err != nil {
		return err
	}
	if len(snapshot.Messages) == 0 && len(snapshot.Turns) == 0 {
		_, err := fmt.Fprintln(out, "(no conversation activity yet)")
		return err
	}
	turnState := make(map[string]string, len(snapshot.Turns))
	for _, turn := range snapshot.Turns {
		turnState[turn.ID] = turn.State
	}
	for _, msg := range snapshot.Messages {
		state := turnState[msg.TurnID]
		if state != "" {
			state = " turn:" + state
		}
		if _, err := fmt.Fprintf(out, "[%s]%s %s\n", msg.Role, state, msg.Text); err != nil {
			return err
		}
	}
	return nil
}
