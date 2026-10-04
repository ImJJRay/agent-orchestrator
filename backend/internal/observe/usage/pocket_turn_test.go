package usage

import (
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestParseCodexNativeTurnIdentity(t *testing.T) {
	for _, tt := range []struct{ name, context, last, ending, want string }{
		{"exact", `{"turn_id":"native-1","model":"gpt-5"}`, `,"last_token_usage":{"input_tokens":10,"output_tokens":2}`, "", "native-1"},
		{"legacy", `{"model":"gpt-5"}`, `,"last_token_usage":{"input_tokens":10,"output_tokens":2}`, "", ""},
		{"cumulative-only", `{"turn_id":"native-1"}`, "", "", ""},
		{"inconsistent-last-vector", `{"turn_id":"native-1"}`, `,"last_token_usage":{"input_tokens":7,"output_tokens":2}`, "", ""},
		{"damaged-boundary", `{"turn_id":"native-1"}`, `,"last_token_usage":{"input_tokens":10,"output_tokens":2}`, `{broken`, ""},
		{"completed", `{"turn_id":"native-1"}`, `,"last_token_usage":{"input_tokens":10,"output_tokens":2}`, `{"type":"event_msg","payload":{"type":"task_complete"}}`, ""},
		{"new-turn-without-context", `{"turn_id":"native-1"}`, `,"last_token_usage":{"input_tokens":10,"output_tokens":2}`, `{"type":"event_msg","payload":{"type":"task_started"}}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := usageSource(domain.UsageSourceCodexRollout)
			first := parseRecords(source, []jsonlRecord{{Data: []byte(`{"type":"turn_context","payload":` + tt.context + `}`)}}, 100, time.Now())
			source.Source.ParserStateJSON = first.Cursor.ParserStateJSON
			records := []jsonlRecord{}
			if tt.ending != "" {
				records = append(records, jsonlRecord{Offset: 100, Data: []byte(tt.ending)})
			}
			records = append(records, jsonlRecord{Offset: 200, Data: []byte(fmt.Sprintf(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2}%s}}}`, tt.last))})
			result := parseRecords(source, records, 300, time.Now())
			if len(result.Events) != 1 || result.Events[0].NativeTurnID != tt.want {
				t.Fatalf("events: %+v", result.Events)
			}
		})
	}
}
