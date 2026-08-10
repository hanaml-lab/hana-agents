"""Hold a submit that cannot be undone until the filled form has been checked against the task.

``RuleVerifier`` compares the form with the task's parameters, the same structured facts the
agent was given. ``ModelVerifier`` asks a second model call to compare them, for tasks whose
parameters are only stated in prose.
"""

from __future__ import annotations

import json
import os
import urllib.request
from dataclasses import dataclass
from typing import Protocol


@dataclass(frozen=True)
class Check:
    ok: bool
    reason: str = ""


class Verifier(Protocol):
    def check(self, instruction: str, params: dict, form_values: dict) -> Check: ...


def _norm(v) -> str:
    return " ".join(str(v).split()).casefold()


class RuleVerifier:
    def check(self, instruction: str, params: dict, form_values: dict) -> Check:
        wrong = [f"{k} is {form_values[k]!r}, the task says {v!r}" for k, v in params.items()
                 if k in form_values and _norm(form_values[k]) != _norm(v)]
        return Check(not wrong, "; ".join(wrong))


class ModelVerifier:
    def __init__(self, model: str, endpoint: str | None = None):
        self.model = model
        self.endpoint = (endpoint or os.environ.get("OPENAI_BASE_URL", "http://localhost:8000/v1")).rstrip("/")

    def check(self, instruction: str, params: dict, form_values: dict) -> Check:
        prompt = (f"Task: {instruction}\nForm about to be submitted: {json.dumps(form_values)}\n"
                  'Does the form do exactly what the task asks? Reply JSON {"ok": bool, "reason": str}.')
        body = {"model": self.model, "temperature": 0, "response_format": {"type": "json_object"},
                "messages": [{"role": "user", "content": prompt}]}
        req = urllib.request.Request(f"{self.endpoint}/chat/completions", data=json.dumps(body).encode(),
                                     headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=120) as resp:
            out = json.loads(json.load(resp)["choices"][0]["message"]["content"])
        return Check(bool(out.get("ok")), out.get("reason", ""))
