"""Observations: a pruned accessibility tree, truncated to a token budget, focused node first.

Off-screen nodes are dropped *before* truncating. Truncating first and pruning afterwards
spends the budget on nodes the agent cannot see. ``harness/a11y-snapshot.ts`` applies the same
rules to a Chromium accessibility tree.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Node:
    ref: str
    role: str
    name: str
    y: int  # top, in page pixels
    value: str = ""
    focused: bool = False

    def line(self) -> str:
        val = f' value="{self.value}"' if self.value else ""
        return f"[{self.ref}] {self.role} \"{self.name}\"{val}"


def tokens(line: str) -> int:
    return max(1, len(line) // 4)


def prune(nodes: list[Node], scroll_y: int, viewport: int) -> list[Node]:
    return [n for n in nodes if scroll_y <= n.y < scroll_y + viewport]


def render(nodes: list[Node], scroll_y: int = 0, viewport: int = 900, budget: int = 2048) -> str:
    visible = prune(nodes, scroll_y, viewport)
    visible.sort(key=lambda n: (not n.focused, n.y))
    out, used = [], 0
    for n in visible:
        line = n.line()
        cost = tokens(line)
        if used + cost > budget:
            out.append(f"... {len(visible) - len(out)} more nodes")
            break
        out.append(line)
        used += cost
    hidden = len(nodes) - len(visible)
    if hidden:
        out.append(f"({hidden} nodes off screen; scroll to see them)")
    return "\n".join(out)
