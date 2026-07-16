package runtime

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/critic"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/journal"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/planner"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

// outbox is an external system with an idempotent API: a repeated key returns the first result.
type outbox struct {
	mu      sync.Mutex
	byKey   map[string]int
	effects int
	calls   int
}

func newOutbox() *outbox { return &outbox{byKey: map[string]int{}} }

func (o *outbox) tool(name string) Tool {
	return Func{ToolName: name, Fn: func(_ context.Context, key string, args map[string]any) (any, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		o.calls++
		if n, ok := o.byKey[key]; ok {
			return map[string]any{"id": n, "deduplicated": true}, nil
		}
		o.effects++
		o.byKey[key] = o.effects
		return map[string]any{"id": o.effects, "to": args["to"]}, nil
	}}
}

var plan = []Step{
	{ID: "read", Tool: "ledger.read", Accept: []critic.Check{{Path: "rows", Op: "nonempty"}}},
	{ID: "draft", Tool: "email.send", Args: map[string]any{"to": "ap@supplier.example", "rows": "${steps.read.rows}"},
		Accept: []critic.Check{{Path: "id", Op: "gte", Value: 1.0}}},
	{ID: "notify", Tool: "email.send", Args: map[string]any{"to": "finance@hanaml.example"}},
}

func ledger() Tool {
	return Func{ToolName: "ledger.read", Fn: func(context.Context, string, map[string]any) (any, error) {
		return map[string]any{"rows": []any{"INV-1", "INV-2"}, "_usage": map[string]any{"tokens": 120}}, nil
	}}
}

func TestRunToCompletion(t *testing.T) {
	ctx := context.Background()
	box := newOutbox()
	e := NewEngine(journal.NewMem(), planner.Static{}, nil)
	id, err := e.Start(ctx, Job{Goal: "g", Tools: []Tool{ledger(), box.tool("email.send")}, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Run(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res), `"to":"finance@hanaml.example"`) || box.effects != 2 {
		t.Fatalf("res=%s effects=%d", res, box.effects)
	}
	st, _ := e.State(ctx, id)
	if st.Tokens != 120 || st.StepsUsed != 3 {
		t.Fatalf("tokens=%d steps=%d", st.Tokens, st.StepsUsed)
	}
}

// crashingJournal fails one append, as if the process died at that point.
type crashingJournal struct {
	journal.Journal
	mu      sync.Mutex
	failAt  int
	appends int
}

var errCrash = errors.New("process killed")

func (c *crashingJournal) Append(ctx context.Context, ev journal.Event) (journal.Event, error) {
	c.mu.Lock()
	c.appends++
	n := c.appends
	c.mu.Unlock()
	if n == c.failAt {
		return ev, errCrash
	}
	return c.Journal.Append(ctx, ev)
}

// TestCrashSuite kills the runtime at a random journal write in each of 200 jobs, resumes
// with a fresh engine, and checks every job completes with exactly one external effect per
// effectful step.
func TestCrashSuite(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		box := newOutbox()
		mem := journal.NewMem()
		cj := &crashingJournal{Journal: mem, failAt: 2 + rng.Intn(16)}
		e1 := NewEngine(cj, planner.Static{}, nil)
		id, err := e1.Start(ctx, Job{Goal: "g", Tools: []Tool{ledger(), box.tool("email.send")}, Plan: plan})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = e1.Run(ctx, id) // may die anywhere
		e2 := NewEngine(mem, planner.Static{}, nil)
		e2.Register(ledger(), box.tool("email.send"))
		if _, err := e2.Run(ctx, id); err != nil {
			t.Fatalf("job %d (crash at write %d): %v", i, cj.failAt, err)
		}
		if box.effects != 2 {
			t.Fatalf("job %d (crash at write %d): %d external effects, want 2", i, cj.failAt, box.effects)
		}
	}
}

func TestReplayNeverCallsTools(t *testing.T) {
	ctx := context.Background()
	box := newOutbox()
	mem := journal.NewMem()
	e := NewEngine(mem, planner.Static{}, nil)
	id, _ := e.Start(ctx, Job{Tools: []Tool{ledger(), box.tool("email.send")}, Plan: plan})
	if _, err := e.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	calls := box.calls
	e2 := NewEngine(mem, planner.Static{}, nil)
	e2.Register(ledger(), box.tool("email.send"))
	if _, err := e2.Run(ctx, id); err != nil || box.calls != calls {
		t.Fatalf("finished job re-ran tools: err=%v calls %d -> %d", err, calls, box.calls)
	}
}

func TestCriticSendsStepBackWithFailedCheck(t *testing.T) {
	ctx := context.Background()
	empty := Func{ToolName: "search", Fn: func(_ context.Context, _ string, args map[string]any) (any, error) {
		if args["q"] == "broad" {
			return map[string]any{"hits": []any{"a"}}, nil
		}
		return map[string]any{"hits": []any{}}, nil
	}}
	p := []Step{{ID: "s", Tool: "search", Args: map[string]any{"q": "narrow"},
		Accept:       []critic.Check{{Path: "hits", Op: "nonempty"}},
		Alternatives: []Step{{Tool: "search", Args: map[string]any{"q": "broad"}, Accept: []critic.Check{{Path: "hits", Op: "nonempty"}}}}}}
	e := NewEngine(journal.NewMem(), planner.Static{Steps: p}, nil)
	id, _ := e.Start(ctx, Job{Tools: []Tool{empty}, Plan: p})
	if _, err := e.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	evs, _ := e.Journal.Load(ctx, id)
	var returned journal.Event
	for _, ev := range evs {
		if ev.Kind == journal.StepReturned {
			returned = ev
		}
	}
	if !strings.Contains(string(returned.Payload), `"failed_check":{"path":"hits","op":"nonempty"}`) {
		t.Fatalf("return event lacks the failed check: %s", returned.Payload)
	}
}

func TestPolicyHoldThenApprovalBoundToArgs(t *testing.T) {
	ctx := context.Background()
	set, err := decide.Compile(map[string]string{"p.cedar": `
		@id("read") permit (principal, action == Action::"ledger.read", resource);
		@hold("outbound-email") permit (principal, action == Action::"email.send", resource)
		when { context.to like "*@supplier.example" };
		@id("internal-email") permit (principal, action == Action::"email.send", resource)
		when { context.to like "*@hanaml.example" };`})
	if err != nil {
		t.Fatal(err)
	}
	box := newOutbox()
	e := NewEngine(journal.NewMem(), planner.Static{}, decide.New(set))
	id, _ := e.Start(ctx, Job{Principal: "agent:ap-clerk", Tools: []Tool{ledger(), box.tool("email.send")}, Plan: plan})
	if _, err := e.Run(ctx, id); !errors.Is(err, ErrAwaitingApproval) {
		t.Fatalf("want a hold, got %v", err)
	}
	st, _ := e.State(ctx, id)
	if st.Pending == nil || st.Pending.Rule != "outbound-email" || box.effects != 0 {
		t.Fatalf("pending=%+v effects=%d", st.Pending, box.effects)
	}
	if err := e.Approve(ctx, id, "maria"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	if box.effects != 2 {
		t.Fatalf("effects=%d", box.effects)
	}
}

func TestPolicyForbidFailsAfterReturns(t *testing.T) {
	ctx := context.Background()
	set, _ := decide.Compile(map[string]string{"p.cedar": `@id("read") permit (principal, action == Action::"ledger.read", resource);`})
	box := newOutbox()
	e := NewEngine(journal.NewMem(), planner.Static{}, decide.New(set))
	id, _ := e.Start(ctx, Job{Tools: []Tool{ledger(), box.tool("email.send")}, Plan: plan})
	_, err := e.Run(ctx, id)
	if !errors.Is(err, ErrJobFailed) || !strings.Contains(err.Error(), "default-deny") || box.effects != 0 {
		t.Fatalf("err=%v effects=%d", err, box.effects)
	}
}

func TestStepBudget(t *testing.T) {
	ctx := context.Background()
	box := newOutbox()
	e := NewEngine(journal.NewMem(), planner.Static{}, nil)
	id, _ := e.Start(ctx, Job{Tools: []Tool{ledger(), box.tool("email.send")}, Plan: plan, Budget: Budget{Steps: 2}})
	if _, err := e.Run(ctx, id); !errors.Is(err, ErrJobFailed) || box.effects != 1 {
		t.Fatalf("err=%v effects=%d", err, box.effects)
	}
}

func TestClientStartWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	box := newOutbox()
	c := NewClient(ctx, NewEngine(journal.NewMem(), planner.Static{}, nil), 4)
	ids := make([]string, 10)
	for i := range ids {
		id, err := c.Start(ctx, Job{Goal: fmt.Sprint(i), Tools: []Tool{ledger(), box.tool("email.send")}, Plan: plan})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	for _, id := range ids {
		if _, err := c.Wait(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if box.effects != 20 {
		t.Fatalf("effects=%d", box.effects)
	}
}

func TestULID(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 26 || a == b {
		t.Fatalf("%s %s", a, b)
	}
}
