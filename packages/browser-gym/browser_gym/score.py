"""Scoring on what the site's database holds, step by step and at the end."""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class StepRecord:
    step: int
    action: str
    ok: bool
    error: str = ""
    progress: float = 0.0  # share of required values present in the database or the open forms
    blocked: str = ""  # reason the verifier held a submit
    restored: bool = False


@dataclass
class Score:
    success: bool
    steps: int
    irreversible_errors: int
    progress: float
    trace: list[StepRecord] = field(default_factory=list)

    @property
    def first_failure(self) -> int | None:
        """The step after which progress never rose again: tells a late failure from an early one."""
        best, at = 0.0, None
        for r in self.trace:
            if r.progress > best:
                best, at = r.progress, None
            elif at is None and not r.ok:
                at = r.step
        return None if self.success else at

    def __str__(self) -> str:
        return (f"success={self.success} steps={self.steps} irreversible_errors={self.irreversible_errors} "
                f"progress={self.progress:.2f}")


def _matches(row: dict, where: dict) -> bool:
    return all(str(row.get(k)) == str(v) for k, v in where.items())


def check_db(db: dict[str, list[dict]], checks: list[dict], initial: dict[str, list[dict]]) -> bool:
    for c in checks:
        rows = db.get(c["table"], [])
        if "expect" in c:
            hits = [r for r in rows if _matches(r, c.get("where", {}))]
            if not any(_matches(r, c["expect"]) for r in hits):
                return False
        if "count" in c:
            if len([r for r in rows if _matches(r, c.get("where", {}))]) != c["count"]:
                return False
        if "unchanged_except" in c:
            key = c.get("key", "id")
            before = {r[key]: r for r in initial.get(c["table"], [])}
            for r in rows:
                if r.get(key) in c["unchanged_except"]:
                    continue
                if r.get(key) in before and r != before[r[key]]:
                    return False
    return True


def progress(db: dict, forms: dict, checks: list[dict]) -> float:
    wanted = [(c["table"], c.get("where", {}), k, v) for c in checks if "expect" in c for k, v in c["expect"].items()]
    if not wanted:
        return 0.0
    got = 0
    for table, where, k, v in wanted:
        in_db = any(_matches(r, where) and str(r.get(k)) == str(v) for r in db.get(table, []))
        in_form = any(f.table == table and str(f.values.get(k)) == str(v) for f in forms.values())
        got += in_db or in_form
    return got / len(wanted)
