package runtime

import (
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/journal"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/planner"
)

// StaticPlanner follows a plan written in advance; a returned step moves to its next alternative.
type StaticPlanner = planner.Static

// ChatPlanner plans with a model behind an OpenAI-compatible endpoint.
type ChatPlanner = planner.Chat

// Event kinds callers may want to match on.
const (
	EventToolResult     = journal.ToolResult
	EventApprovalNeeded = journal.ApprovalNeeded
	EventJobDone        = journal.JobDone
	EventJobFailed      = journal.JobFailed
)

// NewMemJournal returns an in-process journal.
func NewMemJournal() Journal { return journal.NewMem() }

// NewFileJournal returns a journal of one fsynced JSON Lines file per job under dir.
func NewFileJournal(dir string) (Journal, error) { return journal.NewFile(dir) }
