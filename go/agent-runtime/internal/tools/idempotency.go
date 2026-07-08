// Package tools makes tool calls safe to repeat.
//
// Every call carries a key derived from the job, the step and the step's attempt. The attempt
// only advances when the critic sends a step back for a deliberate redo, never on a crash, so
// a resumed job calls with the same key it used before. A result already in the journal is
// returned without calling the tool; a call that was started but has no recorded result is
// made again with the same key, and the tool is expected to deduplicate on it.
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/journal"
)

// Tool is anything the runtime can call.
type Tool interface {
	Name() string
	Call(ctx context.Context, key string, args map[string]any) (json.RawMessage, error)
}

// Key derives the idempotency key for one attempt of one step.
func Key(jobID, stepID string, attempt int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", jobID, stepID, attempt)))
	return hex.EncodeToString(sum[:16])
}

// JournalError means the call could not be recorded. The runtime stops instead of treating it
// as a tool failure; the resume repeats the call with the same key.
type JournalError struct{ Err error }

func (e *JournalError) Error() string { return "journal: " + e.Err.Error() }
func (e *JournalError) Unwrap() error { return e.Err }

// Caller wraps tool calls with the journal.
type Caller struct {
	J journal.Journal
}

// Results indexes recorded tool results by key. Replay must look results up by key, never by
// their position in the journal: re-planning can change which step runs at which position.
func Results(events []journal.Event) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, ev := range events {
		if ev.Kind == journal.ToolResult {
			out[ev.Key] = ev.Payload
		}
	}
	return out
}

// Call returns the recorded result for key, or calls the tool and records the result.
func (c Caller) Call(ctx context.Context, events []journal.Event, jobID, stepID string, attempt int,
	tool Tool, args map[string]any) (json.RawMessage, bool, error) {
	key := Key(jobID, stepID, attempt)
	if res, ok := Results(events)[key]; ok {
		return res, true, nil
	}
	if _, err := c.J.Append(ctx, journal.Event{JobID: jobID, Kind: journal.ToolCalled, StepID: stepID,
		Attempt: attempt, Key: key, Payload: journal.Payload(map[string]any{"tool": tool.Name(), "args": args})}); err != nil {
		return nil, false, &JournalError{err}
	}
	res, err := tool.Call(ctx, key, args)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, err // interrupted: record nothing, the resume will call again with the same key
		}
		_, jerr := c.J.Append(ctx, journal.Event{JobID: jobID, Kind: journal.ToolFailed, StepID: stepID,
			Attempt: attempt, Key: key, Payload: journal.Payload(err.Error())})
		if jerr != nil {
			return nil, false, &JournalError{jerr}
		}
		return nil, false, err
	}
	if _, err := c.J.Append(ctx, journal.Event{JobID: jobID, Kind: journal.ToolResult, StepID: stepID,
		Attempt: attempt, Key: key, Payload: res}); err != nil {
		return nil, false, &JournalError{err}
	}
	return res, false, nil
}
