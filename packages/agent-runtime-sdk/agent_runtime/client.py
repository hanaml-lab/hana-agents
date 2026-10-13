"""A small client for runtimed's job API."""

from __future__ import annotations

import json
import time
import urllib.request
from typing import Any


class JobFailed(RuntimeError):
    pass


class Client:
    def __init__(self, base_url: str = "http://localhost:7070", timeout: float = 30):
        self.base = base_url.rstrip("/")
        self.timeout = timeout

    def _req(self, method: str, path: str, body: Any = None) -> Any:
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.base + path, data=data, method=method,
                                     headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=self.timeout) as resp:
            return json.load(resp)

    def start(self, goal: str, plan: list[dict] | None = None, principal: str = "", budget: dict | None = None,
              context: dict | None = None, untrusted: list[str] | None = None) -> str:
        """Start a job. ``untrusted`` names tools whose output the policy should treat as untrusted input."""
        job = {"goal": goal, "principal": principal, "plan": plan or [], "budget": budget or {},
               "context": context or {}, "untrusted": untrusted or []}
        return self._req("POST", "/jobs", job)["id"]

    def status(self, job_id: str) -> dict:
        return self._req("GET", f"/jobs/{job_id}")

    def events(self, job_id: str, after: int = 0) -> list[dict]:
        return self._req("GET", f"/jobs/{job_id}/events?after={after}")

    def approve(self, job_id: str, approver: str) -> dict:
        return self._req("POST", f"/jobs/{job_id}/approve", {"approver": approver})

    def reject(self, job_id: str, approver: str, reason: str) -> dict:
        """Refuse the held call; the job sends that step back to its planner with the reason."""
        return self._req("POST", f"/jobs/{job_id}/reject", {"approver": approver, "reason": reason})

    def wait(self, job_id: str, timeout: float = 600, poll: float = 0.2) -> dict:
        """Block until the job is done, failed, or waiting for an approval; return its status."""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            st = self.status(job_id)
            if st["status"] == "failed":
                raise JobFailed(st.get("error", ""))
            if st["status"] in {"done", "waiting_approval"}:
                return st
            time.sleep(poll)
        raise TimeoutError(job_id)
