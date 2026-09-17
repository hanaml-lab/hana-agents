# browser-gym

Part of [hana-agents](../../README.md). `browser-gym serve` exposes the environment as tools, so a browser task runs
as a durable `agent-runtime` job with `policy-engine` holding irreversible submits.

An environment for testing agents that click, type and submit forms. Each task runs against its own fresh copy of a
seeded site and database, and is scored on what the site's database holds at the end, not on what the agent says it
did. It also ships the two recovery strategies we found most useful: a verifier that checks an action before it is
irreversible, and backtracking to the last page state that was known to be good.

## Install

```bash
# from the root of hana-agents
uv sync                                      # or: pip install ./packages/agent-runtime-sdk ./packages/browser-gym
docker build -f packages/browser-gym/docker/Dockerfile.chromium -t browser-gym .   # a disposable container per run
```

## Quickstart

```python
from browser_gym import Env, agents

env = Env(task="forms/change-billing-address", observe=["a11y"], verifier=True)
agent = agents.VLM(model="Qwen/Qwen2.5-VL-72B-Instruct", verifier=True, backtrack=True, env=env)
# or, with no model: agent = agents.Heuristic(env=env)

obs = env.reset(seed=7)
while not env.done:
    action = agent.act(obs)
    obs = env.step(action)

print(env.score())   # success, steps, irreversible_errors, per-step trace
```

```bash
browser-gym run --suite tasks/ --agent packages/browser-gym/configs/vlm-verifier.yaml --workers 8 --out runs/
browser-gym report runs/<folder>
```

`agents.VLM` calls the model through an OpenAI-compatible endpoint (`OPENAI_BASE_URL`).

## How it works

- **Observation.** A pruned accessibility tree, truncated to a token budget, with the focused element first; off-screen nodes are dropped before truncating. `harness/a11y-snapshot.ts` applies the same rules to a Chromium accessibility tree.
- **Actions.** Click, type, select, scroll, go back, and `submit`, which is held for the verifier when the form cannot be undone.
- **Verifier.** Before an irreversible submit, the filled form is compared against the task (by rule, or by a second model call). A mismatch blocks the submit and returns the reason to the agent.
- **Backtracking.** Page state is snapshotted after each step that passes its check. When the agent gets lost, the environment restores the last good snapshot instead of starting over. Submitted data is never rolled back.
- **Scoring.** Each task file says which database rows must exist at the end. Trajectories are also scored step by step, so a run that fails late is told apart from one that fails early.

Tasks live in `tasks/forms`, `tasks/shopping` and `tasks/admin`; each file holds the site (pages, forms, seeded
rows) and the checks on the final database.

## Benchmarks

Same VLM in both columns, 50-step limit.

| Metric | Plain agent | With verifier and backtracking |
| --- | --- | --- |
| WebArena task success | 31.5% | 39.8% |
| OSWorld success (screenshot + a11y tree) | 18.2% | 23.9% |
| Tasks ending in a wrong irreversible submit | 9.1% | 2.4% |
| Mean steps per successful task | 14.2 | 17.6 |

Recovery costs steps. Most of the gain is in fewer wrong submits, not in more tasks finished.

## Licence

Apache-2.0.
