"""browser-gym run --suite tasks/forms --agent heuristic --workers 4 --out runs/
browser-gym report runs/<folder>
browser-gym serve --port 7071        serve the environment as agent-runtime tools"""

from __future__ import annotations

import argparse
import json
import sys
from concurrent.futures import ProcessPoolExecutor
from datetime import datetime
from pathlib import Path

import yaml

from browser_gym import agents
from browser_gym.env import TASKS, Env


def build_agent(config: dict, env: Env):
    kind = config.get("agent", "heuristic")
    if kind == "heuristic":
        return agents.Heuristic(mistakes=config.get("mistakes"), backtrack=config.get("backtrack", False), env=env)
    if kind == "vlm":
        return agents.VLM(config["model"], config.get("verifier", True), config.get("backtrack", True), env=env)
    raise SystemExit(f"unknown agent {kind}")


def run_task(args: tuple[str, dict, int]) -> dict:
    task, config, seed = args
    env = Env(task, observe=config.get("observe", ["a11y"]), verifier=config.get("verifier", False))
    agent = build_agent(config, env)
    obs = env.reset(seed=seed)
    while not env.done:
        obs = env.step(agent.act(obs))
    s = env.score()
    return {"task": env.task["id"], "seed": seed, "success": s.success, "steps": s.steps,
            "irreversible_errors": s.irreversible_errors, "progress": s.progress,
            "first_failure": s.first_failure, "trace": [r.__dict__ for r in s.trace]}


def tasks_in(suite: str) -> list[str]:
    path = Path(suite)
    if not path.exists():
        path = TASKS / suite.removeprefix("tasks").lstrip("/")
    if path.is_file():
        return [str(path)]
    if not path.is_dir():
        raise SystemExit(f"no suite {suite}")
    return [str(p) for p in sorted(path.rglob("*.yaml"))]


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="browser-gym")
    sub = ap.add_subparsers(dest="cmd", required=True)
    run = sub.add_parser("run")
    run.add_argument("--suite", default=str(TASKS))
    run.add_argument("--agent", default=None, help="agent config YAML; default: heuristic agent")
    run.add_argument("--workers", type=int, default=1)
    run.add_argument("--seeds", type=int, default=1)
    run.add_argument("--out", default="runs/")
    rep = sub.add_parser("report")
    rep.add_argument("run_dir")
    srv = sub.add_parser("serve")
    srv.add_argument("--host", default="127.0.0.1")
    srv.add_argument("--port", type=int, default=7071)
    args = ap.parse_args(argv)

    if args.cmd == "serve":
        from browser_gym.serve import main as serve_main

        return serve_main(args.host, args.port)
    if args.cmd == "run":
        config = yaml.safe_load(Path(args.agent).read_text()) if args.agent else {"agent": "heuristic"}
        jobs = [(t, config, seed) for t in tasks_in(args.suite) for seed in range(args.seeds)]
        if args.workers > 1:
            with ProcessPoolExecutor(args.workers) as pool:
                results = list(pool.map(run_task, jobs))
        else:
            results = [run_task(j) for j in jobs]
        out = Path(args.out) / datetime.now().strftime("%Y-%m-%dT%H-%M-%S")
        out.mkdir(parents=True)
        (out / "config.json").write_text(json.dumps(config, indent=2))
        with (out / "results.jsonl").open("w") as f:
            for r in results:
                f.write(json.dumps(r) + "\n")
        print(out)
        return 0
    rows = [json.loads(line) for line in (Path(args.run_dir) / "results.jsonl").read_text().splitlines() if line]
    n = len(rows) or 1
    solved = [r for r in rows if r["success"]]
    print(json.dumps({
        "runs": len(rows),
        "success": round(len(solved) / n, 3),
        "wrong_irreversible_submit": round(sum(r["irreversible_errors"] > 0 for r in rows) / n, 3),
        "mean_steps_per_success": round(sum(r["steps"] for r in solved) / max(len(solved), 1), 1),
        "failed_tasks": sorted({r["task"] for r in rows if not r["success"]}),
    }, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
