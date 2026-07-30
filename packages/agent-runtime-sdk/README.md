# agent-runtime-sdk

Python SDK for [agent-runtime](../../go/agent-runtime): serve Python functions as runtime tools, with results
stored by idempotency key, and start, follow and approve jobs over runtimed's HTTP API.

```python
from agent_runtime import Client, ToolServer, tool

@tool("ledger.read")
def read_ledger(period: str) -> dict:
    return {"rows": load_rows(period)}

ToolServer([read_ledger], results="tool-results.sqlite").serve(port=7071)
```

```python
client = Client("http://localhost:7070")
job = client.start("Reconcile September", plan=[{"id": "read", "tool": "ledger.read", "args": {"period": "2026-09"}}])
print(client.wait(job))
```

Apache-2.0.
