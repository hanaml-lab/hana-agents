"""The action space: click, type, select, scroll, back, submit, stop."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field

KINDS = {"click", "type", "select", "scroll", "back", "submit", "stop"}


@dataclass(frozen=True)
class Action:
    kind: str
    target: str = ""  # a node ref from the observation, e.g. "link:ACC-1042" or "field:street"
    text: str = ""
    dy: int = 0
    meta: dict = field(default_factory=dict, compare=False)

    def __post_init__(self):
        if self.kind not in KINDS:
            raise ValueError(f"unknown action {self.kind!r}; one of {sorted(KINDS)}")

    def __str__(self) -> str:
        if self.kind == "type":
            return f"type({self.target}, {self.text!r})"
        if self.kind == "select":
            return f"select({self.target}, {self.text!r})"
        if self.kind == "scroll":
            return f"scroll({self.dy})"
        return f"{self.kind}({self.target})" if self.target else f"{self.kind}()"


_CALL = re.compile(r"^\s*(\w+)\((.*)\)\s*$", re.S)


def parse(text: str) -> Action:
    """Parse ``click(link:ACC-1042)``, ``type(field:city, "Leeds")``, ``scroll(400)`` or a JSON object."""
    text = text.strip()
    if text.startswith("{"):
        d = json.loads(text)
        return Action(d["kind"], d.get("target", ""), d.get("text", ""), int(d.get("dy", 0)))
    m = _CALL.match(text)
    if not m:
        raise ValueError(f"cannot parse action {text!r}")
    kind, inner = m.group(1), m.group(2).strip()
    if kind == "scroll":
        return Action("scroll", dy=int(inner or 0))
    if kind in {"type", "select"}:
        target, _, value = inner.partition(",")
        return Action(kind, target.strip(), json.loads(value.strip()) if value.strip().startswith('"')
                      else value.strip())
    return Action(kind, inner)
