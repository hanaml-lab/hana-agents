# hana-agents

A durable runtime for long agent jobs, with permission rules at the tool boundary and a browser environment to test
agents that use the web. Jobs survive crashes without repeating side effects; every tool call is allowed, held for a
person, or forbidden by written policy before it runs; and browser tasks are scored on what the site's database holds
at the end.

| Component | Language | What it does |
| --- | --- | --- |
| [`agent-runtime`](go/agent-runtime) | Go | Journalled planner, workers and critic; resumes after a crash, reusing recorded tool results by idempotency key; approvals and budgets. |
| [`policy-engine`](go/policy-engine) | Go, TypeScript, TLA+ | Cedar policies decide allow, hold or forbid for every tool call; scoped tokens, argument-bound approvals, time windows, audit log. |
| [`browser-gym`](packages/browser-gym) | Python, TypeScript | Browser tasks on seeded sites, a pre-submit verifier, backtracking, and step-by-step scoring on the final database. |
| [`agent-runtime-sdk`](packages/agent-runtime-sdk) | Python | Serves Python tools to the runtime with idempotent results, and drives jobs over HTTP. |

## Architecture

```mermaid
flowchart LR
    plan([Job: goal, plan, budget]) --> runtimed
    subgraph runtimed [agent-runtime]
        planner[Planner] --> worker[Worker<br/>leases + heartbeats]
        worker --> critic[Critic<br/>acceptance checks]
        critic -->|failed check| planner
        worker <--> journal[(Journal<br/>file or PostgreSQL)]
    end
    worker -->|every tool call| policy[policy-engine<br/>allow / hold / forbid]
    policy -->|hold| person([Approver]) -->|approve digest| worker
    worker -->|key + args| sdk[agent-runtime-sdk<br/>ToolServer]
    sdk --> gym[browser-gym<br/>Env, verifier, backtrack]
    gym --> site[(Seeded site + DB)]
    gym -->|score from DB| worker
```

How the pieces connect:

- `runtime.Engine` calls `decide.Engine.Decide` (policy-engine) before each tool call and journals the decision. A
  hold becomes an `approval_needed` event; `agentctl jobs approve` records an approval bound to
  `approval.Digest(principal, action, args)`, so only that exact call proceeds.
- `browser-gym serve` publishes the environment as `browser.*` tools through `agent_runtime.ToolServer`; runtimed
  discovers them and calls them with idempotency keys, so a resumed job never opens a second session or submits twice.
- `go/policy-engine/policies/examples/browser.cedar` holds every irreversible `browser.submit` for a person.

## Quickstart

```bash
git clone https://github.com/hanaml-lab/hana-agents && cd hana-agents
uv sync              # Python packages
make build           # runtimed, agentctl, policyd into bin/
make example         # a browser task as a runtime job: held submit, approval, score
```

`examples/browser-job/run.sh` starts the browser-gym tool server and runtimed, starts the job in
`examples/browser-job/plan.json`, tails it to the held submit, approves it, and prints the final score. Everything
runs offline. `go/agent-runtime/examples/invoice-reconcile` is an in-process Go example of a back-office job you can
interrupt and resume.

## Development

```bash
make test            # Go unit tests, Python tests, TypeScript tests, and the Go integration test
make crash-suite     # 200 jobs, each killed at a random journal write, checked for exactly-once effects
make lint
```

The integration test (`go/integration`) starts the Python tool server itself; set `HANA_PYTHON` to a Python with
browser-gym installed (`make test-integration` uses the uv environment).

## Related

- Research: [durable agent runs](https://hanaml.com/research/durable-agent-runs),
  [permission rules engine](https://hanaml.com/research/permission-rules-engine),
  [browser agents](https://hanaml.com/research/browser-agents).
- Paper: [The cost of reliability in long agent runs](https://hanaml.com/research/papers/cost-of-reliability-long-runs).
- Model: [llama-toolcall-guard-1b](https://hanaml.com/models/llama-toolcall-guard-1b).
- [hana-evals](https://github.com/hanaml-lab/hana-evals) runs long-job evaluations of agents built on this runtime.

## Licence

Apache-2.0. See `LICENSE`.
