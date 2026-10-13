package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/journal"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/planner"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

const emailPolicy = `
	@id("read") permit (principal, action == Action::"ledger.read", resource);
	@hold("outbound-email") permit (principal, action == Action::"email.send", resource)
	when { context.to like "*@supplier.example" };
	@id("internal-email") permit (principal, action == Action::"email.send", resource)
	when { context.to like "*@hanaml.example" };`

func emailEngine(t *testing.T, p planner.Planner) (*Engine, *outbox) {
	t.Helper()
	set, err := decide.Compile(map[string]string{"p.cedar": emailPolicy})
	if err != nil {
		t.Fatal(err)
	}
	return NewEngine(journal.NewMem(), p, decide.New(set)), newOutbox()
}

func TestRefusedApprovalFailsAStepWithNoAlternative(t *testing.T) {
	ctx := context.Background()
	e, box := emailEngine(t, planner.Static{})
	id, _ := e.Start(ctx, Job{Principal: "agent:ap-clerk", Tools: []Tool{ledger(), box.tool("email.send")}, Plan: plan})
	if _, err := e.Run(ctx, id); !errors.Is(err, ErrAwaitingApproval) {
		t.Fatalf("want a hold, got %v", err)
	}
	if err := e.Reject(ctx, id, "maria", "wrong supplier contact"); err != nil {
		t.Fatal(err)
	}
	_, err := e.Run(ctx, id)
	if !errors.Is(err, ErrJobFailed) || !strings.Contains(err.Error(), "refused by maria: wrong supplier contact") {
		t.Fatalf("err=%v", err)
	}
	if box.effects != 0 {
		t.Fatalf("a refused call ran: effects=%d", box.effects)
	}
}

func TestRefusedApprovalSendsTheStepBackToThePlanner(t *testing.T) {
	ctx := context.Background()
	p := []Step{
		plan[0],
		{ID: "draft", Tool: "email.send", Args: map[string]any{"to": "ap@supplier.example"},
			Alternatives: []Step{{Tool: "email.send", Args: map[string]any{"to": "review@hanaml.example"}}}},
	}
	e, box := emailEngine(t, planner.Static{Steps: p})
	id, _ := e.Start(ctx, Job{Principal: "agent:ap-clerk", Tools: []Tool{ledger(), box.tool("email.send")}, Plan: p})
	if _, err := e.Run(ctx, id); !errors.Is(err, ErrAwaitingApproval) {
		t.Fatalf("want a hold, got %v", err)
	}
	if err := e.Reject(ctx, id, "maria", "send it to review first"); err != nil {
		t.Fatal(err)
	}
	res, err := e.Run(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res), `"to":"review@hanaml.example"`) || box.effects != 1 {
		t.Fatalf("res=%s effects=%d", res, box.effects)
	}
}

func TestRejectNeedsAPendingApproval(t *testing.T) {
	ctx := context.Background()
	e, box := emailEngine(t, planner.Static{})
	id, _ := e.Start(ctx, Job{Tools: []Tool{ledger(), box.tool("email.send")}, Plan: plan[:1]})
	if _, err := e.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := e.Reject(ctx, id, "maria", "no"); err == nil {
		t.Fatal("rejecting a job that waits for nothing should fail")
	}
}
