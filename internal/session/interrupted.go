package session

import "github.com/asayn/asayn/internal/llm/types"

const ToolInterruptedByUser = "Interrupted by user. Execution outcome may be partial or unknown; verify effects before retrying."
const ToolInterruptedBeforeStart = "Interrupted by user before this tool was started."
const ToolInterruptedUnknown = "Previous execution was interrupted before a result was recorded. Execution outcome is unknown; verify effects before retrying."

// RepairInterruptedTools closes incomplete batches before replaying persisted
// history. It never reruns a tool: an unrecorded result does not imply no effects.
func (sess *Session) RepairInterruptedTools() bool {
	var pending []types.ToolCall
	seen := map[string]bool{}
	out := make([]types.ChatMessage, 0, len(sess.Messages))
	changed := false
	flush := func() {
		for _, call := range pending {
			if !seen[call.ID] {
				out = append(out, types.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: ToolInterruptedUnknown})
				changed = true
			}
		}
		pending = nil
		seen = map[string]bool{}
	}
	boundary := sess.CompactedBefore
	for i, msg := range sess.Messages {
		if msg.Role != "tool" {
			flush()
		}
		if i == boundary && boundary > 0 {
			sess.CompactedBefore = len(out)
		}
		out = append(out, msg)
		if msg.Role == "assistant" {
			pending = msg.ToolCalls
		} else if msg.Role == "tool" {
			seen[msg.ToolCallID] = true
		}
	}
	flush()
	if changed {
		sess.Messages = out
	}
	return changed
}
