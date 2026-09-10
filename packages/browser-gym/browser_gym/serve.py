"""The environment as agent-runtime tools, so a browser task runs as a durable runtime job.

Tools are named ``browser.<action>``; each takes the session id returned by ``browser.open``.
``browser.submit`` reports whether the form it would submit is irreversible, so the runtime's
policy can hold it for a person (see policies/examples/browser.cedar in policy-engine).
"""

from __future__ import annotations

import itertools
import threading

from agent_runtime import ToolServer, tool
from browser_gym.actions import Action
from browser_gym.env import Env

_sessions: dict[str, Env] = {}
_ids = itertools.count(1)
_lock = threading.Lock()


def _env(session: str) -> Env:
    try:
        return _sessions[session]
    except KeyError:
        raise ValueError(f"no session {session}") from None


def _obs(env: Env, obs) -> dict:
    return {"url": obs.url, "a11y": obs.a11y, "error": obs.error, "blocked": obs.blocked, "done": env.done}


@tool("browser.open")
def open_task(task: str, seed: int = 0, verifier: bool = True) -> dict:
    env = Env(task, verifier=verifier)
    obs = env.reset(seed=seed)
    with _lock:
        sid = f"s{next(_ids)}"
        _sessions[sid] = env
    return {"session": sid, "instruction": obs.instruction, "params": obs.params, **_obs(env, obs)}


@tool("browser.observe")
def observe(session: str) -> dict:
    env = _env(session)
    return _obs(env, env._obs())


def _act(session: str, action: Action) -> dict:
    env = _env(session)
    return _obs(env, env.step(action))


@tool("browser.click")
def click(session: str, target: str) -> dict:
    return _act(session, Action("click", target))


@tool("browser.type")
def type_(session: str, target: str, text: str) -> dict:
    return _act(session, Action("type", target, text))


@tool("browser.select")
def select(session: str, target: str, text: str) -> dict:
    return _act(session, Action("select", target, text))


@tool("browser.scroll")
def scroll(session: str, dy: int) -> dict:
    return _act(session, Action("scroll", dy=dy))


@tool("browser.back")
def back(session: str) -> dict:
    return _act(session, Action("back"))


@tool("browser.submit")
def submit(session: str, target: str, irreversible: bool = True) -> dict:
    """``irreversible`` is declared by the caller for the policy; the form's own flag decides
    whether the verifier runs, and a mismatch between the two is refused."""
    env = _env(session)
    form = env.site.pending(target)
    if form.irreversible and not irreversible:
        raise ValueError("this form cannot be undone; submit it with irreversible=true")
    return _act(session, Action("submit", target))


@tool("browser.score")
def score(session: str) -> dict:
    s = _env(session).score()
    return {"success": s.success, "steps": s.steps, "irreversible_errors": s.irreversible_errors,
            "progress": s.progress}


TOOLS = [open_task, observe, click, type_, select, scroll, back, submit, score]


def main(host: str = "127.0.0.1", port: int = 7071) -> int:
    server = ToolServer(TOOLS)
    server.serve(host, port, ready=lambda p: print(f"browser-gym tools on http://{host}:{p}", flush=True))
    return 0
