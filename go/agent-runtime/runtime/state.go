package runtime

import (
	"encoding/json"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/journal"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/planner"
)

// Status of a job, as rebuilt from its journal.
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusWaiting Status = "waiting_approval"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Spec is the journalled, serialisable part of a job.
type Spec struct {
	Goal       string         `json:"goal"`
	Principal  string         `json:"principal"`
	Tools      []string       `json:"tools"`
	Budget     Budget         `json:"budget"`
	Plan       []Step         `json:"plan,omitempty"`
	Context    map[string]any `json:"context,omitempty"`
	MaxReturns int            `json:"max_returns_per_step"`
	Untrusted  []string       `json:"untrusted_tools,omitempty"`
}

// State is a job folded from its events. Building it never calls a tool.
type State struct {
	ID        string
	Spec      Spec
	Status    Status
	Plan      []planner.Step
	Results   map[string]json.RawMessage // step id -> accepted result
	Attempt   map[string]int
	Returns   map[string]int
	Started   map[string]bool // "step/attempt" pairs already counted against the budget
	StepsUsed int
	Tokens    int
	Pending   *PendingApproval
	Approved  map[string]string // digest -> approver, not yet used
	Error     string
	Events    int
}

// PendingApproval is a held tool call waiting for a person.
type PendingApproval struct {
	StepID string         `json:"step_id"`
	Action string         `json:"action"`
	Args   map[string]any `json:"args"`
	Digest string         `json:"digest"`
	Rule   string         `json:"rule"`
}

type usage struct {
	Usage struct {
		Tokens int `json:"tokens"`
	} `json:"_usage"`
}

// Fold rebuilds a job's state from its journal.
func Fold(jobID string, events []journal.Event) State {
	st := State{ID: jobID, Status: StatusPending, Results: map[string]json.RawMessage{}, Attempt: map[string]int{},
		Returns: map[string]int{}, Started: map[string]bool{}, Approved: map[string]string{}, Events: len(events)}
	for _, ev := range events {
		switch ev.Kind {
		case journal.JobStarted:
			_ = json.Unmarshal(ev.Payload, &st.Spec)
			st.Status = StatusRunning
		case journal.Planned:
			_ = json.Unmarshal(ev.Payload, &st.Plan)
		case journal.StepStarted:
			k := stepKey(ev.StepID, ev.Attempt)
			if !st.Started[k] {
				st.Started[k] = true
				st.StepsUsed++
			}
		case journal.ApprovalNeeded:
			var p PendingApproval
			_ = json.Unmarshal(ev.Payload, &p)
			st.Pending, st.Status = &p, StatusWaiting
		case journal.Approved:
			var a struct {
				Digest   string `json:"digest"`
				Approver string `json:"approver"`
			}
			_ = json.Unmarshal(ev.Payload, &a)
			st.Approved[a.Digest] = a.Approver
			if st.Pending != nil && st.Pending.Digest == a.Digest {
				st.Pending, st.Status = nil, StatusRunning
			}
		case journal.ToolResult:
			var u usage
			if json.Unmarshal(ev.Payload, &u) == nil {
				st.Tokens += u.Usage.Tokens
			}
		case journal.PolicyDecided:
			var d struct {
				Digest string `json:"digest"`
				Used   bool   `json:"approval_used"`
			}
			if json.Unmarshal(ev.Payload, &d) == nil && d.Used {
				delete(st.Approved, d.Digest)
			}
		case journal.StepReturned:
			var r struct {
				Revised planner.Step `json:"revised"`
			}
			_ = json.Unmarshal(ev.Payload, &r)
			st.Returns[ev.StepID]++
			st.Attempt[ev.StepID] = ev.Attempt + 1
			for i := range st.Plan {
				if st.Plan[i].ID == ev.StepID {
					st.Plan[i] = r.Revised
				}
			}
		case journal.StepDone:
			st.Results[ev.StepID] = ev.Payload
		case journal.JobDone:
			st.Status = StatusDone
		case journal.JobFailed:
			st.Status = StatusFailed
			_ = json.Unmarshal(ev.Payload, &st.Error)
		}
	}
	return st
}

func stepKey(step string, attempt int) string {
	b, _ := json.Marshal([]any{step, attempt})
	return string(b)
}
