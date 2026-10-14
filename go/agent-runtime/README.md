# agent-runtime

Part of [hana-agents](../../README.md). Every tool call is decided by [`policy-engine`](../policy-engine) first;
[`browser-gym`](../../packages/browser-gym) tasks run as jobs through the Python SDK.

Long agent jobs fail halfway: a worker is rescheduled, a model call times out, a deploy restarts the process. An
in-memory agent loop starts again from the top and repeats whatever it had already done, including emails sent and
records written. agent-runtime keeps every step of a job in an append-only journal. After a crash it replays the
journal, reuses the results of steps that finished, and carries on from the step that did not.

## Install

```bash
go install github.com/hanaml-lab/hana-agents/go/agent-runtime/cmd/runtimed@latest
go install github.com/hanaml-lab/hana-agents/go/agent-runtime/cmd/agentctl@latest
docker compose -f go/agent-runtime/deploy/docker-compose.yml up -d   # PostgreSQL, runtimed, policyd, tools
```

Go 1.25 or later. A Python SDK for writing tools lives in `packages/agent-runtime-sdk`.

## Quickstart

```go
job := runtime.Job{
    Goal:   "Reconcile the September invoices against the ledger and flag mismatches",
    Tools:  []runtime.Tool{ledgerRead, invoicesList, emailDraft},
    Budget: runtime.Budget{Tokens: 400_000, Steps: 120, CostUSD: 5, Seconds: 4 * 3600},
    Untrusted: []string{"invoices.list"},   // supplier-written text: arguments from it reach the policy as untrusted
}
client := runtime.NewClient(ctx, runtime.NewEngine(journal, planner, policy), 16)
id, err := client.Start(ctx, job)
if err != nil {
    log.Fatal(err)
}
result, err := client.Wait(ctx, id)   // survives restarts: Engine.Run resumes from the journal
```

`examples/invoice-reconcile` is the full program; run it, interrupt it, and resume it with `-resume <id>`.

```bash
agentctl jobs tail 01J9ZQ...    # watch steps as they are journalled
agentctl jobs replay 01J9ZQ...  # rebuild state from the journal, without calling any tool
agentctl jobs approve 01J9ZQ... -as maria
agentctl jobs reject 01J9ZQ... -as maria -reason "wrong supplier contact"
```

## Architecture

- **Planner** breaks the goal into steps with explicit inputs and outputs (a written plan, or a model through an OpenAI-compatible endpoint).
- **Workers** lease jobs from a queue with heartbeats, call the tools, and write each result to the journal. A worker that stops heartbeating loses its lease and another worker resumes the job.
- **Critic** checks each result against the step's acceptance test and can send a step back to the planner, with the failed check attached.
- **Policy.** Before every tool call the policy engine answers allow, hold or forbid. A hold waits for a person's approval, bound to a digest of the exact arguments and spent once; a refusal sends the step back to the planner with the reason.
- **Provenance.** Tools named in `Job.Untrusted` produce untrusted output. Arguments that derive from it, through any chain of step references, are listed in `context.untrusted_args` for the policy, so a rule can hold a payment whose payee came from an invoice.
- **Budgets** in steps, tokens, cost and wall-clock time, summed from `_usage` in tool results and counted from the `job_started` event, so a restart does not reset them.
- **Journal** is append-only: a JSON Lines file per job, or a PostgreSQL table (`migrations/0001_journal.sql`). Replaying it is deterministic and calls no tool.
- **Idempotency.** Every tool call carries a key derived from the job, the step and the attempt. A result already in the journal is reused; a call that was started but not recorded is repeated with the same key, and tools deduplicate on it (the Python SDK does this for you).

Every step is a span in the OpenTelemetry data model, linked to the job's root trace (`runtimed -traces spans.jsonl`).

## Configuration

```bash
runtimed -journal postgres://runtime@localhost/runtime \
         -policies go/policy-engine/policies/examples \
         -tools http://localhost:7071 \
         -concurrency 16 -lease 90s -heartbeat 15s
```

A job sets `max_returns_per_step` (default 2): how often the critic may send one step back before the job fails.

## Benchmarks

The crash study of the [BELAY paper](https://hanaml.com/research/papers/cost-of-reliability-long-runs) (its Table 1):
200 synthetic back-office jobs, one worker crash injected per job at a random time, five seeds. Kept is the share of
the jobs that succeed without a crash that still succeed with one. `make crash-suite` runs the exactly-once part of
this study offline (200 jobs, a crash at a random journal write, no duplicate effects).

| Recovery | Kept | Duplicate side effects per 100 jobs | Wall-clock overhead |
| --- | --- | --- | --- |
| Restart from scratch (in-memory loop) | 38% | 4.9 | n/a |
| Snapshot every 10 steps | 84% | 1.7 | 1% |
| Journal, no idempotency keys | 96% | 1.3 | 3% |
| Journal and idempotency keys (agent-runtime) | 97% | 0.0 | 3% |

The journal costs no tokens. Where a run should spend tokens on critics and rollbacks is the paper's main question;
its placement program is in the paper repo, and its results are in the [top-level README](../../README.md#results).

## Licence

Apache-2.0.
