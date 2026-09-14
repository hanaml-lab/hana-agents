// Package integration runs a browser-gym task as an agent-runtime job, with policy-engine
// deciding every tool call. It starts the Python tool server, so it needs a Python with
// browser-gym installed: HANA_PYTHON (default "python3").
package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/runtime"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

func toolServer(t *testing.T) string {
	t.Helper()
	py := os.Getenv("HANA_PYTHON")
	if py == "" {
		py = "python3"
	}
	cmd := exec.Command(py, "-m", "browser_gym.cli", "serve", "--port", "0")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start tool server with %s: %v", py, err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		if sc.Scan() {
			line <- sc.Text()
		}
		close(line)
	}()
	select {
	case l, ok := <-line:
		if !ok || !strings.Contains(l, "http://") {
			t.Fatalf("tool server did not start (is browser-gym installed for %s?)", py)
		}
		return l[strings.Index(l, "http://"):]
	case <-time.After(20 * time.Second):
		t.Fatal("tool server did not start in time")
	}
	return ""
}

func loadPlan(t *testing.T, task string) (runtime.Job, []runtime.Step) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "examples", "browser-job", "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Goal      string         `json:"goal"`
		Principal string         `json:"principal"`
		Budget    runtime.Budget `json:"budget"`
		Plan      []runtime.Step `json:"plan"`
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	if task != "" {
		spec.Plan[0].Args["task"] = task
	}
	return runtime.Job{Goal: spec.Goal, Principal: spec.Principal, Budget: spec.Budget, Plan: spec.Plan}, spec.Plan
}

func engine(t *testing.T, j runtime.Journal, base string) *runtime.Engine {
	t.Helper()
	pol, err := decide.Load(filepath.Join("..", "policy-engine", "policies", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	e := runtime.NewEngine(j, runtime.StaticPlanner{}, pol)
	tools, err := runtime.Discover(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	e.Register(tools...)
	return e
}

func TestBrowserTaskAsRuntimeJob(t *testing.T) {
	ctx := context.Background()
	base := toolServer(t)
	e := engine(t, runtime.NewMemJournal(), base)
	job, _ := loadPlan(t, "")
	id, err := e.Start(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Run(ctx, id); !errors.Is(err, runtime.ErrAwaitingApproval) {
		t.Fatalf("the irreversible submit should wait for approval, got %v", err)
	}
	st, _ := e.State(ctx, id)
	if st.Pending.Action != "browser.submit" || st.Pending.Rule != "irreversible-submit" {
		t.Fatalf("pending: %+v", st.Pending)
	}
	if err := e.Approve(ctx, id, "maria"); err != nil {
		t.Fatal(err)
	}
	res, err := e.Run(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var score struct {
		Success            bool `json:"success"`
		IrreversibleErrors int  `json:"irreversible_errors"`
	}
	_ = json.Unmarshal(res, &score)
	if !score.Success || score.IrreversibleErrors != 0 {
		t.Fatalf("score: %s", res)
	}
}

// crashAfter fails the journal append that records a tool result for the given step, as if the
// process died after the tool ran.
type crashAfter struct {
	runtime.Journal
	step string
	once sync.Once
}

func (c *crashAfter) Append(ctx context.Context, ev runtime.Event) (runtime.Event, error) {
	var err error
	if ev.Kind == runtime.EventToolResult && ev.StepID == c.step {
		c.once.Do(func() { err = errors.New("killed") })
	}
	if err != nil {
		return ev, err
	}
	return c.Journal.Append(ctx, ev)
}

func TestCrashAfterSubmitPlacesOneOrder(t *testing.T) {
	ctx := context.Background()
	base := toolServer(t)
	mem := runtime.NewMemJournal()
	job, plan := loadPlan(t, "shopping/order-blue-mugs")
	job.Plan = []runtime.Step{
		plan[0],
		{ID: "product", Tool: "browser.click", Args: map[string]any{"session": "${steps.open.session}", "target": "link:Blue Mug"}},
		{ID: "qty", Tool: "browser.select", Args: map[string]any{"session": "${steps.open.session}", "target": "field:quantity", "text": "2"}},
		{ID: "addr", Tool: "browser.type", Args: map[string]any{"session": "${steps.open.session}", "target": "field:address", "text": "5 Canal Wharf, Leeds"}},
		{ID: "submit", Tool: "browser.submit", Args: map[string]any{"session": "${steps.open.session}", "target": "submit:order", "irreversible": true}},
		plan[len(plan)-1],
	}
	e1 := engine(t, &crashAfter{Journal: mem, step: "submit"}, base)
	id, _ := e1.Start(ctx, job)
	if _, err := e1.Run(ctx, id); !errors.Is(err, runtime.ErrAwaitingApproval) {
		t.Fatal(err)
	}
	_ = e1.Approve(ctx, id, "maria")
	if _, err := e1.Run(ctx, id); err == nil {
		t.Fatal("expected the injected crash")
	}
	e2 := engine(t, mem, base) // a fresh process resumes from the journal
	res, err := e2.Run(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res), `"success": true`) && !strings.Contains(string(res), `"success":true`) {
		t.Fatalf("score after resume: %s (the task checks that exactly one order exists)", res)
	}
}
