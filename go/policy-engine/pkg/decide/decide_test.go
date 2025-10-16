package decide

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/approval"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/capability"
)

var monday = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

func finance(t *testing.T) *Engine {
	t.Helper()
	e, err := Load("../../policies/examples")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestReadmeExample(t *testing.T) {
	d := finance(t).Decide(context.Background(), Request{
		Principal: "agent:ap-clerk", Action: "payments.send",
		Args:    map[string]any{"amount": 4200, "payee": "acct-118"},
		Context: map[string]any{"untrusted_payees": []string{"acct-999"}},
		Now:     monday,
	})
	if d.Outcome != Hold || d.Rule != "over-limit" {
		t.Fatalf("got %s %s", d.Outcome, d.Rule)
	}
}

func TestStrictestRuleWins(t *testing.T) {
	e := finance(t)
	ctx := map[string]any{"untrusted_payees": []string{"acct-118"}}
	d := e.Decide(context.Background(), Request{Principal: "agent:x", Action: "payments.send",
		Args: map[string]any{"amount": 4200, "payee": "acct-118"}, Context: ctx, Now: monday})
	if d.Outcome != Forbid {
		t.Fatalf("forbid and hold both match; want forbid, got %s by %s", d.Outcome, d.Rule)
	}
	small := e.Decide(context.Background(), Request{Principal: "agent:x", Action: "payments.send",
		Args: map[string]any{"amount": 50, "payee": "acct-1"}, Context: map[string]any{"untrusted_payees": []string{}}, Now: monday})
	if small.Outcome != Allow || small.Rule != "within-limit" {
		t.Fatalf("got %s %s", small.Outcome, small.Rule)
	}
}

func TestDefaultDenyAndMissingAttribute(t *testing.T) {
	e := finance(t)
	if d := e.Decide(context.Background(), Request{Action: "payments.refund", Now: monday}); d.Outcome != Forbid || d.Rule != "default-deny" {
		t.Fatalf("got %s %s", d.Outcome, d.Rule)
	}
	// amount missing: the conditions error, nothing permits, so it is denied
	if d := e.Decide(context.Background(), Request{Action: "payments.send", Now: monday,
		Context: map[string]any{"untrusted_payees": []string{}}}); d.Outcome != Forbid {
		t.Fatalf("got %s", d.Outcome)
	}
}

func TestTimeWindow(t *testing.T) {
	saturday := time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC)
	d := finance(t).Decide(context.Background(), Request{Action: "payments.send", Now: saturday,
		Args: map[string]any{"amount": 10, "payee": "a"}, Context: map[string]any{"untrusted_payees": []string{}}})
	if d.Outcome != Forbid || d.Rule != "weekend-payments" {
		t.Fatalf("got %s %s", d.Outcome, d.Rule)
	}
}

func TestCapabilityBeforePolicy(t *testing.T) {
	key := []byte("test-key")
	s, _ := capability.Sign(capability.Token{Principal: "agent:x", Actions: []string{"invoices.*"}}, key)
	tok, err := capability.Verify(s, key)
	if err != nil {
		t.Fatal(err)
	}
	d := finance(t).Decide(context.Background(), Request{Action: "payments.send", Token: tok, Now: monday,
		Args: map[string]any{"amount": 10}})
	if d.Outcome != Forbid || d.Rule != "capability" {
		t.Fatalf("got %s %s", d.Outcome, d.Rule)
	}
	if _, err := capability.Verify(s[:len(s)-2]+"xx", key); err == nil {
		t.Fatal("tampered token verified")
	}
}

func TestApprovalBoundToArguments(t *testing.T) {
	st := approval.NewStore()
	a, _ := approval.Digest("agent:x", "payments.send", map[string]any{"amount": 4200, "payee": "acct-118"})
	b, _ := approval.Digest("agent:x", "payments.send", map[string]any{"amount": 4300, "payee": "acct-119"})
	st.Approve(approval.Approval{Digest: a, Approver: "maria"})
	if _, err := st.Consume(b, monday); err == nil {
		t.Fatal("approval for other arguments accepted")
	}
	if _, err := st.Consume(a, monday); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Consume(a, monday); err == nil {
		t.Fatal("approval used twice")
	}
}

func TestParseErrorsNamePosition(t *testing.T) {
	_, err := Parse("bad.cedar", "permit (principal, action == Action::\"a\", resource)\nwhen { context.x > };")
	if err == nil || !strings.Contains(err.Error(), "bad.cedar:2") {
		t.Fatalf("got %v", err)
	}
}

func TestTenantCacheReusesCompiledSet(t *testing.T) {
	tn := NewTenants()
	src := map[string]string{"a.cedar": `permit (principal, action, resource);`}
	e1, _ := tn.Get("t1", src)
	e2, _ := tn.Get("t1", src)
	if e1 != e2 {
		t.Fatal("same text recompiled")
	}
	e3, _ := tn.Get("t1", map[string]string{"a.cedar": `forbid (principal, action, resource);`})
	if e3 == e1 {
		t.Fatal("changed text not recompiled")
	}
}

// TestTLATraces replays every state TLC enumerates for spec/Decide.tla.
func TestTLATraces(t *testing.T) {
	raw, err := os.ReadFile("testdata/tla-traces.json")
	if err != nil {
		t.Fatal(err)
	}
	var traces struct {
		Cases []struct {
			InScope bool     `json:"inScope"`
			Matched []string `json:"matched"`
			Outcome string   `json:"outcome"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &traces); err != nil {
		t.Fatal(err)
	}
	rules := map[string]string{
		"permit": `@id("p") permit (principal, action == Action::"act", resource) when { context.permit == true };`,
		"hold":   `@hold("h") permit (principal, action == Action::"act", resource) when { context.hold == true };`,
		"forbid": `@id("f") forbid (principal, action == Action::"act", resource) when { context.forbid == true };`,
	}
	src := strings.Join([]string{rules["permit"], rules["hold"], rules["forbid"]}, "\n")
	set, err := Compile(map[string]string{"t.cedar": src})
	if err != nil {
		t.Fatal(err)
	}
	e := New(set)
	for i, c := range traces.Cases {
		ctx := map[string]any{"permit": false, "hold": false, "forbid": false}
		for _, m := range c.Matched {
			ctx[m] = true
		}
		scope := []string{"other"}
		if c.InScope {
			scope = []string{"act"}
		}
		d := e.Decide(context.Background(), Request{Action: "act", Context: ctx, Now: monday,
			Token: &capability.Token{Actions: scope}})
		if d.Outcome.String() != c.Outcome {
			t.Errorf("case %d %+v: got %s", i, c, d.Outcome)
		}
	}
	if len(traces.Cases) != 16 {
		t.Fatalf("want 16 states, got %d", len(traces.Cases))
	}
}
