package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/journal"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/planner"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

// summarise is a model call that reports what it cost, as the Python SDK's tools do in _usage.
func summarise(calls *int) Tool {
	return Func{ToolName: "model.summarise", Fn: func(context.Context, string, map[string]any) (any, error) {
		*calls++
		return map[string]any{"summary": "ok", "_usage": map[string]any{"tokens": 900, "cost_usd": 0.03}}, nil
	}}
}

var threeSummaries = []Step{
	{ID: "a", Tool: "model.summarise"}, {ID: "b", Tool: "model.summarise"}, {ID: "c", Tool: "model.summarise"},
}

func TestCostBudgetStopsBeforeTheCallThatWouldExceedIt(t *testing.T) {
	ctx := context.Background()
	calls := 0
	e := NewEngine(journal.NewMem(), planner.Static{}, nil)
	id, _ := e.Start(ctx, Job{Tools: []Tool{summarise(&calls)}, Plan: threeSummaries, Budget: Budget{CostUSD: 0.05}})
	_, err := e.Run(ctx, id)
	if !errors.Is(err, ErrJobFailed) || !strings.Contains(err.Error(), "cost budget of $0.05 exhausted") {
		t.Fatalf("err=%v", err)
	}
	st, _ := e.State(ctx, id)
	if calls != 2 || st.CostUSD < 0.0599 || st.CostUSD > 0.0601 {
		t.Fatalf("calls=%d cost=%v", calls, st.CostUSD)
	}
}

func TestTimeBudgetCountsFromTheJobStart(t *testing.T) {
	ctx := context.Background()
	calls := 0
	e := NewEngine(journal.NewMem(), planner.Static{}, nil)
	id, _ := e.Start(ctx, Job{Tools: []Tool{summarise(&calls)}, Plan: threeSummaries, Budget: Budget{Seconds: 60}})
	e.Now = func() time.Time { return time.Now().Add(2 * time.Minute) } // the job resumes two minutes later
	_, err := e.Run(ctx, id)
	if !errors.Is(err, ErrJobFailed) || !strings.Contains(err.Error(), "time budget of 60s exhausted") || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

// A rule limited to the first hour of a job needs the job's start time; the runtime supplies it.
func TestPolicyWindowsSeeTheJobStart(t *testing.T) {
	ctx := context.Background()
	set, err := decide.Compile(map[string]string{"p.cedar": `
		@id("first-hour") @window("first 1h")
		permit (principal, action == Action::"model.summarise", resource);`})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	e := NewEngine(journal.NewMem(), planner.Static{}, decide.New(set))
	id, _ := e.Start(ctx, Job{Tools: []Tool{summarise(&calls)}, Plan: threeSummaries[:1]})
	if _, err := e.Run(ctx, id); err != nil {
		t.Fatalf("inside the first hour: %v", err)
	}
	late, _ := e.Start(ctx, Job{Tools: []Tool{summarise(&calls)}, Plan: threeSummaries[:1]})
	e.Now = func() time.Time { return time.Now().Add(90 * time.Minute) }
	if _, err := e.Run(ctx, late); !errors.Is(err, ErrJobFailed) || calls != 1 {
		t.Fatalf("after the first hour: err=%v calls=%d", err, calls)
	}
}
