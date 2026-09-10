from agent_runtime import ToolServer
from browser_gym.serve import TOOLS


def test_tools_drive_a_task_and_keys_deduplicate():
    server = ToolServer(TOOLS)
    opened = server.call("browser.open", "k-open", {"task": "admin/raise-credit-limit"})["result"]
    sid = opened["session"]
    again = server.call("browser.open", "k-open", {"task": "admin/raise-credit-limit"})["result"]
    assert again["session"] == sid  # a replayed open does not start a second session
    server.call("browser.click", "k1", {"session": sid, "target": "link:Fenwick Cleaning"})
    server.call("browser.type", "k2", {"session": sid, "target": "field:credit_limit", "text": "7500"})
    refused = server.call("browser.submit", "k3", {"session": sid, "target": "submit:credit", "irreversible": False})
    assert "cannot be undone" in refused["error"]
    done = server.call("browser.submit", "k4", {"session": sid, "target": "submit:credit", "irreversible": True})
    assert done["result"]["done"]
    assert server.call("browser.score", "k5", {"session": sid})["result"]["success"]
