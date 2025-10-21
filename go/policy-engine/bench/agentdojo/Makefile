# Replays AgentDojo tool-call traces through the policy engine.
#
# Export traces from an AgentDojo run (banking suite, with and without injections) with
# export_traces.py, then: make -C bench/agentdojo TRACES=path/to/traces.jsonl
TRACES ?= sample-traces.jsonl

.PHONY: replay
replay:
	go run . -traces $(TRACES) -policies policies/
