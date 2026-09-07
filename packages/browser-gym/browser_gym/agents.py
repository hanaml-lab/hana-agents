"""Agents. ``Heuristic`` needs no model and is the reference agent in tests; ``VLM`` calls a
vision-language model behind an OpenAI-compatible endpoint."""

from __future__ import annotations

import json
import os
import re
import urllib.request

from browser_gym.actions import Action, parse
from browser_gym.env import Observation

_REF = re.compile(r"^\[([^\]]+)\] (\w+) \"([^\"]*)\"(?: value=\"([^\"]*)\")?", re.M)


def nodes(obs: Observation) -> list[tuple[str, str, str, str]]:
    return [(m.group(1), m.group(2), m.group(3), m.group(4) or "") for m in _REF.finditer(obs.a11y)]


def _key(text: str) -> str:
    return re.sub(r"[^a-z0-9]", "", text.lower())


class Heuristic:
    """Follows links that name a task parameter, fills fields whose label names one, submits.

    ``mistakes`` maps a field to a wrong value the agent will type first, as agents do when they
    copy the value next to the right one. A mistake is corrected only when a held submit names
    that field, which is what the verifier's feedback is for.
    """

    def __init__(self, mistakes: dict | None = None, backtrack: bool = False, env=None):
        self.mistakes = {_key(k): str(v) for k, v in (mistakes or {}).items()}
        self.backtrack, self.env = backtrack, env
        self.visited: set[str] = set()
        self.failures = 0

    def act(self, obs: Observation) -> Action:
        if obs.error or obs.blocked:
            self.failures += 1
        for field in list(self.mistakes):
            if field in _key(obs.blocked):
                del self.mistakes[field]
        if self.backtrack and self.env is not None and self.failures >= 2:
            self.failures = 0
            self.env.backtrack()
            return Action("scroll", dy=0)
        items = nodes(obs)
        params = {_key(k): str(v) for k, v in obs.params.items()}
        params.update(self.mistakes)
        for ref, role, name, value in items:
            if role in {"textbox", "combobox"}:
                field = ref.removeprefix("field:")
                want = params.get(_key(field)) or params.get(_key(name))
                if want is not None and value != want:
                    return Action("select" if role == "combobox" else "type", ref, want)
        for ref, role, _name, _ in items:
            if role == "button":
                return Action("submit", ref)
        for ref, role, name, _ in items:
            if role == "link" and ref not in self.visited and any(v.lower() in name.lower() or name.lower() in v.lower()
                                                                   for v in params.values() if len(v) > 2):
                self.visited.add(ref)
                return Action("click", ref)
        unvisited = [ref for ref, role, _n, _v in items if role == "link" and ref not in self.visited]
        if len(unvisited) == 1:  # a page with one way forward
            self.visited.add(unvisited[0])
            return Action("click", unvisited[0])
        if "off screen" in obs.a11y:
            return Action("scroll", dy=400)
        return Action("stop")


class VLM:
    PROMPT = ("You operate a web browser to complete a task. Reply with one action, one of: click(<ref>), "
              'type(<ref>, "text"), select(<ref>, "option"), scroll(<pixels>), back(), submit(<ref>), stop().')

    def __init__(self, model: str, verifier: bool = True, backtrack: bool = True, endpoint: str | None = None,
                 env=None):
        self.model, self.verifier, self.backtrack, self.env = model, verifier, backtrack, env
        self.endpoint = (endpoint or os.environ.get("OPENAI_BASE_URL", "http://localhost:8000/v1")).rstrip("/")
        self.history: list[str] = []

    def act(self, obs: Observation) -> Action:
        note = f"\nLast action failed: {obs.error}" if obs.error else ""
        note += f"\nThe submit was held: {obs.blocked}" if obs.blocked else ""
        text = (f"Task: {obs.instruction}\nPage: {obs.url}\n{obs.a11y}{note}\n"
                f"Previous actions: {'; '.join(self.history[-8:]) or 'none'}")
        body = {"model": self.model, "temperature": 0,
                "messages": [{"role": "system", "content": self.PROMPT}, {"role": "user", "content": text}]}
        req = urllib.request.Request(f"{self.endpoint}/chat/completions", data=json.dumps(body).encode(),
                                     headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=120) as resp:
            reply = json.load(resp)["choices"][0]["message"]["content"].strip().splitlines()[-1]
        action = parse(reply)
        self.history.append(str(action))
        return action
