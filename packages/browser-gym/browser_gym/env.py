"""The environment loop: reset, step, score."""

from __future__ import annotations

import copy
import random
from dataclasses import dataclass
from pathlib import Path

import yaml

from browser_gym.actions import Action, parse
from browser_gym.backtrack import Backtracker
from browser_gym.observe import render
from browser_gym.score import Score, StepRecord, check_db, progress
from browser_gym.site import ActionError, Site
from browser_gym.verify import RuleVerifier, Verifier

_HERE = Path(__file__).resolve().parent
TASKS = _HERE / "tasks" if (_HERE / "tasks").is_dir() else _HERE.parent / "tasks"  # installed wheel, or a checkout


@dataclass
class Observation:
    url: str
    a11y: str
    instruction: str
    params: dict
    error: str = ""
    blocked: str = ""
    screenshot: bytes | None = None


def load_task(task: str | Path) -> dict:
    path = Path(task)
    if not path.exists():
        path = TASKS / task
        if path.suffix != ".yaml":
            path = path.with_suffix(".yaml")
    spec = yaml.safe_load(path.read_text(encoding="utf-8"))
    spec.setdefault("id", path.stem)
    return spec


class Env:
    """One task in one disposable site: a fresh copy of the task's seeded site and database
    on every reset."""

    def __init__(self, task: str | Path | dict, observe: list[str] | None = None, verifier: Verifier | bool = False,
                 max_steps: int | None = None, token_budget: int = 2048):
        self.task = task if isinstance(task, dict) else load_task(task)
        self.observe = observe or ["a11y"]
        self.verifier: Verifier | None = RuleVerifier() if verifier is True else (verifier or None)
        self.max_steps = max_steps or self.task.get("max_steps", 50)
        self.token_budget = token_budget
        self.backtracker = Backtracker()
        self.site: Site | None = None

    # ------------------------------------------------------------------------------------
    def reset(self, seed: int = 0) -> Observation:
        spec = copy.deepcopy(self.task["site"])
        rng = random.Random(seed)
        for rows in spec.get("db", {}).values():  # seeds shuffle row order, which link order follows
            rng.shuffle(rows)
        self.site = Site(spec)
        self._initial_db = copy.deepcopy(self.site.state.db)
        self.trace: list[StepRecord] = []
        self.irreversible_errors = 0
        self.stopped = False
        self.backtracker = Backtracker()
        self.backtracker.mark_good(self.site)
        return self._obs()

    @property
    def done(self) -> bool:
        if self.site is None:
            return False
        submitted_final = any(s["irreversible"] for s in self.site.state.submitted) and self.task.get(
            "done_on_irreversible_submit", True)
        return self.stopped or submitted_final or len(self.trace) >= self.max_steps

    def _obs(self, error: str = "", blocked: str = "") -> Observation:
        assert self.site is not None
        a11y = render(self.site.nodes(), self.site.state.scroll_y, budget=self.token_budget) \
            if "a11y" in self.observe else ""
        return Observation(self.site.state.path, a11y, self.task["instruction"], self.task.get("params", {}),
                           error, blocked)

    def step(self, action: Action | str) -> Observation:
        if self.site is None:
            raise RuntimeError("call reset() first")
        if self.done:
            raise RuntimeError("episode is over")
        act = parse(action) if isinstance(action, str) else action
        rec = StepRecord(len(self.trace) + 1, str(act), True)
        error = blocked = ""
        try:
            self._apply(act, rec)
        except ActionError as exc:
            rec.ok, error = False, str(exc)
        blocked = rec.blocked
        if rec.ok and not rec.blocked:
            self.backtracker.mark_good(self.site)
        rec.error = error
        rec.progress = progress(self.site.state.db, self.site.state.forms, self.task.get("checks", []))
        self.trace.append(rec)
        return self._obs(error, blocked)

    def _apply(self, act: Action, rec: StepRecord) -> None:
        s = self.site
        if act.kind == "click":
            s.click(act.target)
        elif act.kind == "type":
            s.type(act.target, act.text)
        elif act.kind == "select":
            s.select(act.target, act.text)
        elif act.kind == "scroll":
            s.scroll(act.dy)
        elif act.kind == "back":
            s.back()
        elif act.kind == "stop":
            self.stopped = True
        elif act.kind == "submit":
            form = s.pending(act.target)
            if form.irreversible and self.verifier is not None:
                verdict = self.verifier.check(self.task["instruction"], self.task.get("params", {}), form.values)
                if not verdict.ok:
                    rec.ok, rec.blocked = False, verdict.reason
                    return
            record = s.submit(act.target)
            if record["irreversible"] and not check_db(s.state.db, self.task.get("checks", []), self._initial_db):
                self.irreversible_errors += 1

    def backtrack(self) -> Observation:
        """Restore the last good page state. The database keeps whatever was submitted."""
        assert self.site is not None
        restored = self.backtracker.restore(self.site)
        if self.trace:
            self.trace[-1].restored = restored
        return self._obs("" if restored else "no earlier good state")

    def score(self) -> Score:
        assert self.site is not None
        ok = check_db(self.site.state.db, self.task.get("checks", []), self._initial_db)
        prog = self.trace[-1].progress if self.trace else 0.0
        return Score(ok, len(self.trace), self.irreversible_errors, prog, list(self.trace))
