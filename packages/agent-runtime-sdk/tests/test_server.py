import json
import threading
import urllib.request

from agent_runtime import ToolServer, tool


@tool("email.send")
def send(to: str, body: str = "") -> dict:
    return {"sent_to": to}


@tool("boom")
def boom() -> dict:
    raise ValueError("no ledger for that period")


def post(port, name, payload):
    req = urllib.request.Request(f"http://127.0.0.1:{port}/tools/{name}", data=json.dumps(payload).encode(),
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as r:
        return json.load(r)


def test_repeated_key_returns_stored_result():
    server = ToolServer([send, boom])
    httpd, port = server.start()
    try:
        assert post(port, "email.send", {"key": "k1", "args": {"to": "a@x"}}) == {"result": {"sent_to": "a@x"}}
        assert post(port, "email.send", {"key": "k1", "args": {"to": "a@x"}})["result"] == {"sent_to": "a@x"}
        assert server.calls == 1
        post(port, "email.send", {"key": "k2", "args": {"to": "a@x"}})
        assert server.calls == 2
        assert "no ledger" in post(port, "boom", {"key": "k3", "args": {}})["error"]
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/tools") as r:
            assert json.load(r) == ["boom", "email.send"]
    finally:
        httpd.shutdown()


def test_concurrent_deliveries_run_once(tmp_path):
    server = ToolServer([send], results=str(tmp_path / "r.sqlite"))
    threads = [threading.Thread(target=server.call, args=("email.send", "same", {"to": "b@x"})) for _ in range(8)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert server.calls == 1
    again = ToolServer([send], results=str(tmp_path / "r.sqlite"))  # a restarted server remembers
    again.call("email.send", "same", {"to": "b@x"})
    assert again.calls == 0
