import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from agent_runtime.client import Client


class Runtimed(BaseHTTPRequestHandler):
    """Records what the client sends, and answers as runtimed does."""

    seen: list = []

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        Runtimed.seen.append((self.path, body))
        reply = {"id": "job-1"}
        if self.path.endswith("/reject"):
            reply = {"id": "job-1", "refused_by": body["approver"]}
        data = json.dumps(reply).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *args):
        pass


def serve():
    Runtimed.seen = []
    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Runtimed)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return httpd, Client(f"http://127.0.0.1:{httpd.server_address[1]}")


def test_start_declares_untrusted_tools():
    httpd, client = serve()
    try:
        client.start("pay the invoice", plan=[{"id": "invoice", "tool": "inbox.read"}], untrusted=["inbox.read"])
        path, job = Runtimed.seen[0]
        assert path == "/jobs"
        assert job["untrusted"] == ["inbox.read"]
    finally:
        httpd.shutdown()


def test_reject_sends_the_approver_and_the_reason():
    httpd, client = serve()
    try:
        assert client.reject("job-1", "maria", "payee changed by email")["refused_by"] == "maria"
        assert Runtimed.seen[0] == ("/jobs/job-1/reject", {"approver": "maria", "reason": "payee changed by email"})
    finally:
        httpd.shutdown()
