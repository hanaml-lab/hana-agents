package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/worker"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/runtime"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

const policy = `
	@id("read") permit (principal, action == Action::"inbox.read", resource);
	@id("pay") permit (principal, action == Action::"payments.send", resource);
	@hold("untrusted-payee") permit (principal, action == Action::"payments.send", resource)
	when { "payee" in context.untrusted_args };`

func server(t *testing.T) (*httptest.Server, *runtime.Engine) {
	t.Helper()
	set, err := decide.Compile(map[string]string{"p.cedar": policy})
	if err != nil {
		t.Fatal(err)
	}
	e := runtime.NewEngine(runtime.NewMemJournal(), runtime.StaticPlanner{}, decide.New(set))
	e.Register(
		runtime.Func{ToolName: "inbox.read", Fn: func(context.Context, string, map[string]any) (any, error) {
			return map[string]any{"payee": "acct-999"}, nil
		}},
		runtime.Func{ToolName: "payments.send", Fn: func(context.Context, string, map[string]any) (any, error) {
			return map[string]any{"paid": true}, nil
		}},
	)
	srv := httptest.NewServer(routes(e, worker.NewMem()))
	t.Cleanup(srv.Close)
	return srv, e
}

func call(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestUntrustedToolsAndRefusalOverHTTP(t *testing.T) {
	srv, e := server(t)
	code, out := call(t, "POST", srv.URL+"/jobs", `{"goal": "pay the invoice", "untrusted": ["inbox.read"], "plan": [
		{"id": "invoice", "tool": "inbox.read"},
		{"id": "pay", "tool": "payments.send", "args": {"payee": "${steps.invoice.payee}", "amount": 100}}]}`)
	if code != http.StatusAccepted {
		t.Fatalf("start: %d %v", code, out)
	}
	id := out["id"].(string)
	if _, err := e.Run(context.Background(), id); err != runtime.ErrAwaitingApproval {
		t.Fatalf("want a hold on the untrusted payee, got %v", err)
	}
	if code, out = call(t, "POST", srv.URL+"/jobs/"+id+"/reject", `{"approver": "maria"}`); code != http.StatusBadRequest {
		t.Fatalf("a refusal without a reason: %d %v", code, out)
	}
	code, out = call(t, "POST", srv.URL+"/jobs/"+id+"/reject", `{"approver": "maria", "reason": "payee changed by email"}`)
	if code != http.StatusOK || out["refused_by"] != "maria" {
		t.Fatalf("reject: %d %v", code, out)
	}
	_, err := e.Run(context.Background(), id)
	if err == nil || !strings.Contains(err.Error(), "refused by maria: payee changed by email") {
		t.Fatalf("err=%v", err)
	}
}
