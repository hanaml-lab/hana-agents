#!/usr/bin/env bash
# A browser task as a durable runtime job, end to end and offline:
#   browser-gym serves the environment as tools, runtimed runs the plan with policy-engine
#   checking every call, the irreversible submit is held until a person approves it, and the
#   final score comes from the site's database.
#
#   make build && examples/browser-job/run.sh
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
bin="$root/bin"
work="$(mktemp -d)"
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$work"' EXIT

export AGENT_RUNTIME=http://127.0.0.1:7070
uv run --project "$root" browser-gym serve --port 7071 &
for _ in $(seq 50); do curl -sf http://127.0.0.1:7071/tools >/dev/null && break; sleep 0.2; done

"$bin/runtimed" -addr 127.0.0.1:7070 -journal "$work/journal" \
  -policies "$root/go/policy-engine/policies/examples" -tools http://127.0.0.1:7071 &
for _ in $(seq 50); do curl -sf "$AGENT_RUNTIME/jobs" >/dev/null && break; sleep 0.2; done

id=$("$bin/agentctl" jobs start "$here/plan.json" | sed -E 's/.*"id":"([^"]+)".*/\1/')
echo "job $id"
"$bin/agentctl" jobs tail "$id"          # stops at the held submit
"$bin/agentctl" jobs approve "$id" -as maria
"$bin/agentctl" jobs tail "$id"          # runs to the end
"$bin/agentctl" jobs replay "$id"
curl -s "$AGENT_RUNTIME/jobs/$id"
echo
