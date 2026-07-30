"""Serve Python functions as runtime tools.

    from agent_runtime import ToolServer, tool

    @tool("ledger.read")
    def read_ledger(period: str) -> dict:
        ...

    ToolServer([read_ledger], results="tool-results.sqlite").serve(port=7071)

runtimed calls ``POST /tools/<name>`` with ``{"key", "args"}``. The key identifies one attempt
of one step; a repeated key returns the stored result instead of running the function again,
which is what makes a resumed job safe when the process died after the tool ran but before
the runtime recorded the result. With ``results=`` the stored results survive a restart.
"""

from __future__ import annotations

import json
import sqlite3
import threading
from collections.abc import Callable
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


def tool(name: str):
    """Name a function as a runtime tool."""
    def mark(fn: Callable[..., Any]) -> Callable[..., Any]:
        fn.__tool_name__ = name  # type: ignore[attr-defined]
        return fn
    return mark


class ResultStore:
    def __init__(self, path: str = ":memory:"):
        self._db = sqlite3.connect(path, check_same_thread=False)
        self._db.execute("CREATE TABLE IF NOT EXISTS results (key TEXT PRIMARY KEY, tool TEXT, result TEXT)")
        self._lock = threading.Lock()
        self._inflight: dict[str, threading.Lock] = {}

    def get(self, key: str) -> str | None:
        with self._lock:
            row = self._db.execute("SELECT result FROM results WHERE key = ?", (key,)).fetchone()
        return row[0] if row else None

    def put(self, key: str, tool_name: str, result: str) -> None:
        with self._lock:
            self._db.execute("INSERT OR IGNORE INTO results VALUES (?, ?, ?)", (key, tool_name, result))
            self._db.commit()

    def key_lock(self, key: str) -> threading.Lock:
        with self._lock:
            return self._inflight.setdefault(key, threading.Lock())


class ToolServer:
    def __init__(self, tools: list[Callable[..., Any]], results: str = ":memory:"):
        self.tools = {getattr(fn, "__tool_name__", fn.__name__): fn for fn in tools}
        self.store = ResultStore(results)
        self.calls = 0  # function executions, for tests and metrics

    def call(self, name: str, key: str, args: dict) -> dict:
        if name not in self.tools:
            return {"error": f"unknown tool {name}"}
        with self.store.key_lock(key):  # two deliveries of one key never run concurrently
            stored = self.store.get(key)
            if stored is not None:
                return {"result": json.loads(stored)}
            try:
                self.calls += 1
                result = self.tools[name](**args)
            except Exception as exc:  # the runtime records the failure and asks the planner
                return {"error": f"{type(exc).__name__}: {exc}"}
            self.store.put(key, name, json.dumps(result))
            return {"result": result}

    def handler(self):
        server = self

        class Handler(BaseHTTPRequestHandler):
            def _send(self, code: int, body: Any) -> None:
                data = json.dumps(body).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def do_GET(self):
                if self.path.rstrip("/") == "/tools":
                    self._send(200, sorted(server.tools))
                else:
                    self._send(404, {"error": "not found"})

            def do_POST(self):
                if not self.path.startswith("/tools/"):
                    self._send(404, {"error": "not found"})
                    return
                length = int(self.headers.get("Content-Length", 0))
                try:
                    body = json.loads(self.rfile.read(length) or b"{}")
                except json.JSONDecodeError as exc:
                    self._send(400, {"error": str(exc)})
                    return
                name = self.path[len("/tools/"):]
                self._send(200, server.call(name, body.get("key", ""), body.get("args") or {}))

            def log_message(self, *args):  # quiet by default
                pass

        return Handler

    def serve(self, host: str = "127.0.0.1", port: int = 7071, ready: Callable[[int], None] | None = None) -> None:
        httpd = ThreadingHTTPServer((host, port), self.handler())
        if ready:
            ready(httpd.server_address[1])
        try:
            httpd.serve_forever()
        finally:
            httpd.server_close()

    def start(self, host: str = "127.0.0.1", port: int = 0) -> tuple[ThreadingHTTPServer, int]:
        """Serve on a background thread; returns the server and its port."""
        httpd = ThreadingHTTPServer((host, port), self.handler())
        threading.Thread(target=httpd.serve_forever, daemon=True).start()
        return httpd, httpd.server_address[1]
