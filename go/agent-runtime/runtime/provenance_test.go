package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/journal"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/planner"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

// An invoice the agent reads may carry an injected instruction, so its fields are untrusted.
func inbox() Tool {
	return Func{ToolName: "inbox.read", Fn: func(context.Context, string, map[string]any) (any, error) {
		return map[string]any{"payee": "acct-999", "amount": 4200.0, "supplier": "Acme"}, nil
	}}
}

// directory looks a supplier up in the firm's own records; it is trusted, but what it returns
// for an untrusted query is only as good as the query.
func directory() Tool {
	return Func{ToolName: "suppliers.lookup", Fn: func(_ context.Context, _ string, args map[string]any) (any, error) {
		return map[string]any{"account": "acct-118", "name": args["name"]}, nil
	}}
}

const paymentPolicy = `
	@id("read") permit (principal, action in [Action::"inbox.read", Action::"suppliers.lookup"], resource);
	@id("pay") permit (principal, action == Action::"payments.send", resource);
	@hold("untrusted-payee") permit (principal, action == Action::"payments.send", resource)
	when { "payee" in context.untrusted_args };`

func paymentEngine(t *testing.T) (*Engine, *outbox) {
	t.Helper()
	set, err := decide.Compile(map[string]string{"p.cedar": paymentPolicy})
	if err != nil {
		t.Fatal(err)
	}
	return NewEngine(journal.NewMem(), planner.Static{}, decide.New(set)), newOutbox()
}

func TestPayeeReadFromUntrustedContentIsHeld(t *testing.T) {
	ctx := context.Background()
	e, box := paymentEngine(t)
	p := []Step{
		{ID: "invoice", Tool: "inbox.read"},
		{ID: "pay", Tool: "payments.send", Args: map[string]any{"payee": "${steps.invoice.payee}", "amount": 100.0}},
	}
	id, _ := e.Start(ctx, Job{Principal: "agent:ap-clerk", Tools: []Tool{inbox(), box.tool("payments.send")},
		Untrusted: []string{"inbox.read"}, Plan: p})
	if _, err := e.Run(ctx, id); !errors.Is(err, ErrAwaitingApproval) {
		t.Fatalf("want a hold, got %v", err)
	}
	st, _ := e.State(ctx, id)
	if st.Pending == nil || st.Pending.Rule != "untrusted-payee" || box.effects != 0 {
		t.Fatalf("pending=%+v effects=%d", st.Pending, box.effects)
	}
}

func TestPayeeWrittenInThePlanRunsWithoutApproval(t *testing.T) {
	ctx := context.Background()
	e, box := paymentEngine(t)
	p := []Step{
		{ID: "invoice", Tool: "inbox.read"},
		{ID: "pay", Tool: "payments.send", Args: map[string]any{"payee": "acct-118", "amount": "${steps.invoice.amount}"}},
	}
	id, _ := e.Start(ctx, Job{Principal: "agent:ap-clerk", Tools: []Tool{inbox(), box.tool("payments.send")},
		Untrusted: []string{"inbox.read"}, Plan: p})
	if _, err := e.Run(ctx, id); err != nil {
		t.Fatalf("a payee from the plan should not be held: %v", err)
	}
	if box.effects != 1 {
		t.Fatalf("effects=%d", box.effects)
	}
}

// Taint follows data: a trusted tool called with an untrusted argument returns untrusted results.
func TestUntrustedDataStaysUntrustedThroughATrustedTool(t *testing.T) {
	ctx := context.Background()
	e, box := paymentEngine(t)
	p := []Step{
		{ID: "invoice", Tool: "inbox.read"},
		{ID: "supplier", Tool: "suppliers.lookup", Args: map[string]any{"name": "${steps.invoice.supplier}"}},
		{ID: "pay", Tool: "payments.send", Args: map[string]any{"payee": "${steps.supplier.account}", "amount": 100.0}},
	}
	id, _ := e.Start(ctx, Job{Principal: "agent:ap-clerk", Tools: []Tool{inbox(), directory(), box.tool("payments.send")},
		Untrusted: []string{"inbox.read"}, Plan: p})
	if _, err := e.Run(ctx, id); !errors.Is(err, ErrAwaitingApproval) {
		t.Fatalf("want a hold, got %v", err)
	}
}
