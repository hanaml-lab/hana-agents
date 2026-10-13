// Package runtime runs long agent jobs durably. Every step is journalled; after a crash a job
// is replayed from its journal and continues from the step that had not finished, reusing the
// recorded results of tool calls instead of repeating them.
//
// Before every tool call the runtime asks a policy (hana-agents' policy-engine) whether the
// call may run, must wait for a person's approval, or is forbidden.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/critic"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/journal"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/otel"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/planner"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/tools"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/approval"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

type (
	// Tool is anything the runtime can call with an idempotency key.
	Tool = tools.Tool
	// Step is one planned unit of work.
	Step = planner.Step
	// Check is an acceptance condition on a step result.
	Check = critic.Check
	// Planner plans and revises steps.
	Planner = planner.Planner
	// Journal stores events.
	Journal = journal.Journal
	// Event is one journal row.
	Event = journal.Event
)

// Budget caps a job. Zero means unlimited.
type Budget struct {
	Tokens int `json:"tokens"`
	Steps  int `json:"steps"`
}

// Job is what a caller asks the runtime to do.
type Job struct {
	Goal       string
	Principal  string
	Tools      []Tool
	Budget     Budget
	Plan       []Step         // optional: a plan written in advance; otherwise the planner plans
	Context    map[string]any // passed to policy decisions
	MaxReturns int            // how often the critic may send one step back; default 2
	// Untrusted names tools whose output may carry instructions the agent must not follow, such
	// as a mailbox or a browser. Arguments derived from their output reach the policy as
	// context.untrusted_args.
	Untrusted []string
}

// Policy decides tool calls. *decide.Engine satisfies it.
type Policy interface {
	Decide(ctx context.Context, r decide.Request) decide.Decision
}

// AllowAll is a policy for tests and trusted tools only.
type AllowAll struct{}

func (AllowAll) Decide(context.Context, decide.Request) decide.Decision {
	return decide.Decision{Outcome: decide.Allow, Rule: "allow-all"}
}

var (
	ErrAwaitingApproval = errors.New("runtime: job is waiting for an approval")
	ErrJobFailed        = errors.New("runtime: job failed")
	ErrUnknownTool      = errors.New("runtime: unknown tool")
)

// Engine runs jobs against a journal.
type Engine struct {
	Journal Journal
	Planner Planner
	Policy  Policy
	Tracer  *otel.Tracer

	mu    sync.RWMutex
	tools map[string]Tool
	locks sync.Map // job id -> *sync.Mutex: one runner per job per process
}

func NewEngine(j Journal, p Planner, pol Policy) *Engine {
	if pol == nil {
		pol = AllowAll{}
	}
	return &Engine{Journal: j, Planner: p, Policy: pol, tools: map[string]Tool{}}
}

// Register makes tools callable. A process that resumes jobs must register the same tools.
func (e *Engine) Register(ts ...Tool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range ts {
		e.tools[t.Name()] = t
	}
}

func (e *Engine) tool(name string) (Tool, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.tools[name]
	return t, ok
}

// Start journals a new job and returns its id. It does not run it.
func (e *Engine) Start(ctx context.Context, job Job) (string, error) {
	e.Register(job.Tools...)
	names := make([]string, 0, len(job.Tools))
	for _, t := range job.Tools {
		names = append(names, t.Name())
	}
	sort.Strings(names)
	if job.MaxReturns == 0 {
		job.MaxReturns = 2
	}
	id := NewID()
	spec := Spec{Goal: job.Goal, Principal: job.Principal, Tools: names, Budget: job.Budget, Plan: job.Plan,
		Context: job.Context, MaxReturns: job.MaxReturns, Untrusted: job.Untrusted}
	_, err := e.Journal.Append(ctx, journal.Event{JobID: id, Kind: journal.JobStarted, Payload: journal.Payload(spec)})
	return id, err
}

// State replays a job's journal.
func (e *Engine) State(ctx context.Context, id string) (State, error) {
	evs, err := e.Journal.Load(ctx, id)
	if err != nil {
		return State{}, err
	}
	if len(evs) == 0 {
		return State{}, fmt.Errorf("runtime: no job %s", id)
	}
	return Fold(id, evs), nil
}

// Approve records a person's approval for the call the job is waiting on.
func (e *Engine) Approve(ctx context.Context, id, approver string) error {
	st, err := e.State(ctx, id)
	if err != nil {
		return err
	}
	if st.Pending == nil {
		return fmt.Errorf("runtime: job %s is not waiting for an approval", id)
	}
	_, err = e.Journal.Append(ctx, journal.Event{JobID: id, Kind: journal.Approved, StepID: st.Pending.StepID,
		Payload: journal.Payload(map[string]string{"digest": st.Pending.Digest, "approver": approver})})
	return err
}

// Reject records that a person refused the call the job is waiting on. On its next run the job
// sends that step back to the planner with the reason; with no revision to offer, the job fails.
// The refusal is bound to the call's digest, so a revised call with other arguments asks again.
func (e *Engine) Reject(ctx context.Context, id, approver, reason string) error {
	st, err := e.State(ctx, id)
	if err != nil {
		return err
	}
	if st.Pending == nil {
		return fmt.Errorf("runtime: job %s is not waiting for an approval", id)
	}
	_, err = e.Journal.Append(ctx, journal.Event{JobID: id, Kind: journal.Rejected, StepID: st.Pending.StepID,
		Payload: journal.Payload(map[string]string{"digest": st.Pending.Digest, "approver": approver, "reason": reason})})
	return err
}

func (e *Engine) append(ctx context.Context, ev journal.Event) error {
	_, err := e.Journal.Append(ctx, ev)
	return err
}

func (e *Engine) fail(ctx context.Context, id, reason string) error {
	if err := e.append(ctx, journal.Event{JobID: id, Kind: journal.JobFailed, Payload: journal.Payload(reason)}); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s", ErrJobFailed, reason)
}

// Run drives a job forward from wherever its journal says it is, until it finishes, fails or
// waits for an approval. Calling Run again after a crash is always safe.
func (e *Engine) Run(ctx context.Context, id string) (json.RawMessage, error) {
	lock, _ := e.locks.LoadOrStore(id, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	for {
		evs, err := e.Journal.Load(ctx, id)
		if err != nil {
			return nil, err
		}
		st := Fold(id, evs)
		switch st.Status {
		case StatusDone:
			return st.Results[st.Plan[len(st.Plan)-1].ID], nil
		case StatusFailed:
			return nil, fmt.Errorf("%w: %s", ErrJobFailed, st.Error)
		case StatusWaiting:
			return nil, ErrAwaitingApproval
		case StatusPending:
			return nil, fmt.Errorf("runtime: no job %s", id)
		}
		if st.Plan == nil {
			plan := st.Spec.Plan
			if len(plan) == 0 {
				if plan, err = e.Planner.Plan(ctx, st.Spec.Goal, st.Spec.Tools); err != nil {
					return nil, err
				}
			}
			if err := e.append(ctx, journal.Event{JobID: id, Kind: journal.Planned, Payload: journal.Payload(plan)}); err != nil {
				return nil, err
			}
			continue
		}
		next := -1
		for i, s := range st.Plan {
			if _, done := st.Results[s.ID]; !done {
				next = i
				break
			}
		}
		if next < 0 {
			last := st.Plan[len(st.Plan)-1].ID
			if err := e.append(ctx, journal.Event{JobID: id, Kind: journal.JobDone, Payload: st.Results[last]}); err != nil {
				return nil, err
			}
			return st.Results[last], nil
		}
		if err := e.step(ctx, st, evs, st.Plan[next]); err != nil {
			return nil, err
		}
	}
}

// step runs one attempt of one step and journals what happened. Returning nil means "look at
// the journal again": the step finished, was sent back, or the job ended.
func (e *Engine) step(ctx context.Context, st State, evs []journal.Event, s Step) error {
	attempt := st.Attempt[s.ID]
	if !st.Started[stepKey(s.ID, attempt)] {
		if b := st.Spec.Budget; b.Steps > 0 && st.StepsUsed >= b.Steps {
			return e.fail(ctx, st.ID, fmt.Sprintf("step budget of %d exhausted", b.Steps))
		}
		if b := st.Spec.Budget; b.Tokens > 0 && st.Tokens >= b.Tokens {
			return e.fail(ctx, st.ID, fmt.Sprintf("token budget of %d exhausted", b.Tokens))
		}
		if err := e.append(ctx, journal.Event{JobID: st.ID, Kind: journal.StepStarted, StepID: s.ID, Attempt: attempt,
			Payload: journal.Payload(s)}); err != nil {
			return err
		}
	}
	end := e.Tracer.StepSpan(st.ID, s.ID, map[string]string{"tool": s.Tool, "attempt": fmt.Sprint(attempt)})
	status := "ok"
	defer func() { end(status) }()

	args, err := planner.Resolve(s.Args, st.Results)
	if err != nil {
		status = "error"
		return e.fail(ctx, st.ID, fmt.Sprintf("step %s: %v", s.ID, err))
	}
	t, ok := e.tool(s.Tool)
	if !ok {
		status = "error"
		return fmt.Errorf("%w %q (register it with Engine.Register)", ErrUnknownTool, s.Tool)
	}

	// Policy, before every tool call. A recorded result means the call already ran with
	// permission, so a replay does not ask again.
	key := tools.Key(st.ID, s.ID, attempt)
	if _, ran := tools.Results(evs)[key]; !ran && !permitted(evs, s.ID, attempt) {
		polCtx := map[string]any{}
		for k, v := range st.Spec.Context {
			polCtx[k] = v
		}
		polCtx["untrusted_args"] = untrustedArgs(s, st.Plan, st.Spec.Untrusted)
		d := e.Policy.Decide(ctx, decide.Request{Principal: st.Spec.Principal, Action: s.Tool, Args: args, Context: polCtx})
		digest, _ := approval.Digest(st.Spec.Principal, s.Tool, args)
		_, approved := st.Approved[digest]
		record := map[string]any{"outcome": d.Outcome.String(), "rule": d.Rule, "policy_version": d.Version,
			"digest": digest, "approval_used": d.Outcome == decide.Hold && approved}
		if err := e.append(ctx, journal.Event{JobID: st.ID, Kind: journal.PolicyDecided, StepID: s.ID, Attempt: attempt,
			Payload: journal.Payload(record)}); err != nil {
			return err
		}
		switch {
		case d.Outcome == decide.Forbid:
			status = "forbidden"
			return e.sendBack(ctx, st, s, attempt, fmt.Sprintf("policy forbids %s (rule %s)", s.Tool, d.Rule), nil, nil)
		case d.Outcome == decide.Hold && !approved && st.Refused[digest] != "":
			status = "refused"
			return e.sendBack(ctx, st, s, attempt, "approval "+st.Refused[digest], nil, nil)
		case d.Outcome == decide.Hold && !approved:
			status = "held"
			return e.append(ctx, journal.Event{JobID: st.ID, Kind: journal.ApprovalNeeded, StepID: s.ID, Attempt: attempt,
				Payload: journal.Payload(PendingApproval{StepID: s.ID, Action: s.Tool, Args: args, Digest: digest, Rule: d.Rule})})
		}
	}

	res, _, err := tools.Caller{J: e.Journal}.Call(ctx, evs, st.ID, s.ID, attempt, t, args)
	if err != nil {
		var jerr *tools.JournalError
		if ctx.Err() != nil || errors.As(err, &jerr) {
			status = "interrupted"
			return err
		}
		status = "tool_error"
		return e.sendBack(ctx, st, s, attempt, "tool error: "+err.Error(), nil, nil)
	}
	if v := critic.Review(res, s.Accept); !v.OK {
		status = "returned"
		return e.sendBack(ctx, st, s, attempt, v.Reason, v.Failed, res)
	}
	return e.append(ctx, journal.Event{JobID: st.ID, Kind: journal.StepDone, StepID: s.ID, Attempt: attempt, Payload: res})
}

// sendBack returns a step to the planner with the failed check attached.
func (e *Engine) sendBack(ctx context.Context, st State, s Step, attempt int, reason string, failed *critic.Check,
	result json.RawMessage) error {
	returns := st.Returns[s.ID] + 1
	if returns > st.Spec.MaxReturns {
		return e.fail(ctx, st.ID, fmt.Sprintf("step %s: %s (sent back %d times)", s.ID, reason, returns-1))
	}
	revised, err := e.Planner.Revise(ctx, st.Spec.Goal, planner.Feedback{Step: s, Reason: reason, Failed: failed,
		Result: result, Return: returns})
	if errors.Is(err, planner.ErrGiveUp) {
		return e.fail(ctx, st.ID, fmt.Sprintf("step %s: %s", s.ID, reason))
	}
	if err != nil {
		return err
	}
	return e.append(ctx, journal.Event{JobID: st.ID, Kind: journal.StepReturned, StepID: s.ID, Attempt: attempt,
		Payload: journal.Payload(map[string]any{"reason": reason, "failed_check": failed, "revised": revised})})
}

// permitted reports whether this attempt already has a decision that let it run, so a resume
// between the decision and the tool call does not ask again or spend a second approval.
func permitted(evs []journal.Event, stepID string, attempt int) bool {
	for _, ev := range evs {
		if ev.Kind != journal.PolicyDecided || ev.StepID != stepID || ev.Attempt != attempt {
			continue
		}
		var d struct {
			Outcome string `json:"outcome"`
			Used    bool   `json:"approval_used"`
		}
		if json.Unmarshal(ev.Payload, &d) == nil && (d.Outcome == "allow" || d.Used) {
			return true
		}
	}
	return false
}
