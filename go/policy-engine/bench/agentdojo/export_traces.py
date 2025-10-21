"""Export AgentDojo run logs as the JSONL that bench/agentdojo replays.

    python export_traces.py --logdir runs/agentdojo --goals goals.json \
        --known-payees UK12345678901234567890 GB29NWBK60161331926819 > traces.jsonl

AgentDojo writes one JSON log per (user task, injection task) run, with the conversation in
``messages`` and each assistant tool call as ``{"function", "args"}``. ``goals.json`` maps an
injection task id to the functions its attacker goal needs (for banking, e.g.
``{"injection_task_0": ["send_money"]}``). ``--known-payees`` are the recipients in the user's
own history before the task, which is what the banking policy trusts.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--logdir", required=True)
    ap.add_argument("--goals", required=True)
    ap.add_argument("--known-payees", nargs="*", default=[])
    ap.add_argument("--suite", default="banking")
    args = ap.parse_args()
    goals = json.loads(Path(args.goals).read_text())
    for path in sorted(Path(args.logdir).rglob("*.json")):
        run = json.loads(path.read_text())
        if run.get("suite_name") != args.suite:
            continue
        injection = run.get("injection_task_id")
        calls = [{"function": c["function"], "args": c.get("args", {})}
                 for m in run.get("messages", []) for c in (m.get("tool_calls") or [])]
        print(json.dumps({
            "task": run.get("user_task_id"),
            "injection": injection is not None,
            "calls": calls,
            "attacker_goal": goals.get(injection, []) if injection else [],
            "known_payees": args.known_payees,
        }))
    return 0


if __name__ == "__main__":
    sys.exit(main())
