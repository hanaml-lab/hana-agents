# Changelog

## 0.6.0 (2026-10-14)

- agent-runtime: arguments derived from untrusted tool output reach the policy as `context.untrusted_args`
- agent-runtime: refuse a held call (`Engine.Reject`, `POST /jobs/{id}/reject`, `agentctl jobs reject`); the step
  goes back to the planner with the reason
- agent-runtime: cost and time budgets; the policy sees the job's start, so `@window("first 1h")` works under the
  runtime
- agent-runtime-sdk 0.10.0: `Client.reject` and untrusted tools on `Client.start`
- policy-engine: `spec/Approvals.tla`, model-checked with TLC, and `spec/check.sh` with seeded mutants
- README with install, results, papers and citation; CONTRIBUTING, CITATION.cff; CI checks the specifications

## 0.5.0 (2026-10-09)

- browser tasks run as runtime jobs with irreversible submits held by policy
- integration test

## 0.4.0 (2026-09-08)

- browser-gym: site simulator, verifier, backtracking, step-by-step scoring

## 0.3.0 (2026-08-03)

- runtimed and agentctl
- Python SDK for tools

## 0.2.0 (2026-07-16)

- agent-runtime: journal, planner, critic, workers, idempotent tool calls, approvals and budgets

## 0.1.0 (2025-10-20)

- policy-engine: Cedar subset, strictest-wins decisions, capability tokens, argument-bound approvals, audit log
