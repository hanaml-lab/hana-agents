# policy-engine

Part of [hana-agents](../../README.md). `agent-runtime` asks it before every tool call; `clients/ts` is for agents
written in TypeScript.

Prompt-level defences ask the model not to do the wrong thing. policy-engine sits at the tool boundary instead:
every tool call an agent makes is checked against written policy before it runs, and the answer is one of
**allow**, **hold** (wait for a person's approval) or **forbid**. The strictest matching rule wins, and every decision
names the rule that made it, so an audit can say why an action ran.

## Install

```bash
go install github.com/hanaml-lab/hana-agents/go/policy-engine/cmd/policyd@latest
npm install ./go/policy-engine/clients/ts     # TypeScript client, for agents written in TS
```

## Quickstart

```cedar
// policies/examples/finance.cedar
permit (principal, action == Action::"invoices.read", resource);

@hold("over-limit")
permit (principal, action == Action::"payments.send", resource)
when { context.amount > 1000 };

forbid (principal, action == Action::"payments.send", resource)
when { resource.payee in context.untrusted_payees };
```

```go
engine, _ := decide.Load("policies/")
d := engine.Decide(ctx, decide.Request{
    Principal: "agent:ap-clerk",
    Action:    "payments.send",
    Args:      map[string]any{"amount": 4200, "payee": "acct-118"},
    Context:   map[string]any{"untrusted_payees": []string{"acct-999"}},
})
fmt.Println(d.Outcome, d.Rule)   // hold over-limit
```

The engine reads the subset of Cedar shown here: `permit`/`forbid`, a scope on principal and action, `when` and
`unless` conditions with comparisons, `in`, `like`, `&&`, `||` and `!`, and the annotations `@id`, `@hold` and
`@window`. Arguments are visible as both `resource.*` and `context.*`.

## How it works

- **Scoped capabilities.** An agent's token lists the actions and resources it may touch; anything else is forbidden before policy runs.
- **Policies** are Cedar. `forbid` beats `hold`, and `hold` beats `permit`; a request no permit matches is denied.
- **Approvals** are bound to a digest of the exact arguments. Approving a payment of 4,200 to one account does not approve 4,300 to another.
- **Time windows.** `@window("Mon-Fri 09:00-17:30 Europe/Berlin")` or `@window("first 1h")` limits a rule to office hours, or to the first hour of a job.
- **Audit.** Each decision is logged with the request, the outcome, the rule and the policy version, in a hash-chained JSON Lines file.
- **Specification.** `spec/Decide.tla` models the decision table; its states are replayed as Go tests (`pkg/decide/testdata/tla-traces.json`). `spec/Approvals.tla` models how agent-runtime spends approvals and calls tools across crashes and revisions; TLC checks that an effect runs only with an approval of exactly its arguments, that one approval pays for one attempt, that a key takes effect at most once and that a retry is never charged twice. `spec/check.sh` (`make spec`) runs TLC on both and then confirms that each seeded bug in `spec/mutants.txt` is caught by the invariant it names.

## Benchmarks

AgentDojo, same agent and model in both columns. `make -C bench/agentdojo TRACES=traces.jsonl` replays tool calls
exported from an AgentDojo run (`export_traces.py`) through the policies in `bench/agentdojo/policies/`.

| Metric | Prompt-level defence | policy-engine |
| --- | --- | --- |
| Injection attacks leading to a forbidden action | 24% | 0.8% |
| Benign task success | 69% | 64% |
| Policy check latency, p99 | n/a | 2.1 ms |

Stricter policy costs some legitimate tasks. Of 20 seeded policy bugs, unit tests alone caught 11; with the TLA+
traces, 19.

## Licence

Apache-2.0.
