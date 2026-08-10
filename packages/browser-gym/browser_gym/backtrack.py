"""Return to the last page state that was known to be good instead of starting over."""

from __future__ import annotations

from browser_gym.site import Site, SiteState


class Backtracker:
    def __init__(self, limit: int = 20):
        self.stack: list[SiteState] = []
        self.limit = limit

    def mark_good(self, site: Site) -> None:
        """Snapshot after a step that passed its check."""
        self.stack.append(site.snapshot())
        del self.stack[: -self.limit]

    def restore(self, site: Site) -> bool:
        """Restore the most recent good snapshot that differs from where the agent is now."""
        while self.stack:
            snap = self.stack[-1]
            if snap.path != site.state.path or snap.forms != site.state.forms:
                site.restore(snap)
                return True
            self.stack.pop()
        return False
