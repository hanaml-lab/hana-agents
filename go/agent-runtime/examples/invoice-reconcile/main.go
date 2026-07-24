// Invoice reconciliation, end to end: read the September invoices and the ledger, flag the
// mismatches, and draft an email to the supplier. Runs in process with a file journal; run it,
// kill it with Ctrl-C half-way, and run it again with the printed job id to watch it resume.
//
//	go run ./agent-runtime/examples/invoice-reconcile
//	go run ./agent-runtime/examples/invoice-reconcile -resume <job id>
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/runtime"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

type row struct {
	Ref    string  `json:"ref"`
	Amount float64 `json:"amount"`
}

var invoices = []row{{"INV-0901", 1250.00}, {"INV-0902", 310.40}, {"INV-0903", 89.99}, {"INV-0904", 2400.00}}
var ledger = []row{{"INV-0901", 1250.00}, {"INV-0902", 301.40}, {"INV-0904", 2400.00}}

func slow() { time.Sleep(400 * time.Millisecond) } // long enough to interrupt by hand

var tools = []runtime.Tool{
	runtime.Func{ToolName: "invoices.list", Fn: func(context.Context, string, map[string]any) (any, error) {
		slow()
		return map[string]any{"rows": invoices}, nil
	}},
	runtime.Func{ToolName: "ledger.read", Fn: func(context.Context, string, map[string]any) (any, error) {
		slow()
		return map[string]any{"rows": ledger}, nil
	}},
	runtime.Func{ToolName: "reconcile.compare", Fn: func(_ context.Context, _ string, args map[string]any) (any, error) {
		slow()
		book := map[string]float64{}
		for _, r := range args["ledger"].([]any) {
			m := r.(map[string]any)
			book[m["ref"].(string)] = m["amount"].(float64)
		}
		var flagged []string
		for _, r := range args["invoices"].([]any) {
			m := r.(map[string]any)
			amt, ok := book[m["ref"].(string)]
			if !ok || math.Abs(amt-m["amount"].(float64)) > 0.005 {
				flagged = append(flagged, m["ref"].(string))
			}
		}
		return map[string]any{"mismatches": flagged, "count": len(flagged)}, nil
	}},
	runtime.Func{ToolName: "email.draft", Fn: func(_ context.Context, key string, args map[string]any) (any, error) {
		slow()
		body := fmt.Sprintf("Hello,\n\nThese September invoices do not match our ledger: %v.\nCould you send corrected copies?\n", args["mismatches"])
		return map[string]any{"draft_id": "draft-" + key[:8], "to": args["to"], "body": body}, nil
	}},
}

const policy = `
@id("read") permit (principal, action in [Action::"invoices.list", Action::"ledger.read", Action::"reconcile.compare"], resource);
@id("draft-to-supplier") permit (principal, action == Action::"email.draft", resource) when { context.to like "*@*" };
`

func main() {
	resume := flag.String("resume", "", "job id to resume")
	dir := flag.String("journal", filepath.Join(os.TempDir(), "invoice-reconcile-journal"), "journal directory")
	flag.Parse()
	ctx := context.Background()

	j, err := runtime.NewFileJournal(*dir)
	if err != nil {
		log.Fatal(err)
	}
	set, err := decide.Compile(map[string]string{"reconcile.cedar": policy})
	if err != nil {
		log.Fatal(err)
	}
	engine := runtime.NewEngine(j, runtime.StaticPlanner{}, decide.New(set))
	engine.Register(tools...)

	id := *resume
	if id == "" {
		id, err = engine.Start(ctx, runtime.Job{
			Goal:      "Reconcile the September invoices against the ledger and flag mismatches",
			Principal: "agent:ap-clerk",
			Tools:     tools,
			Budget:    runtime.Budget{Tokens: 400_000, Steps: 120},
			Plan: []runtime.Step{
				{ID: "invoices", Tool: "invoices.list", Accept: []runtime.Check{{Path: "rows", Op: "nonempty"}}},
				{ID: "ledger", Tool: "ledger.read", Accept: []runtime.Check{{Path: "rows", Op: "nonempty"}}},
				{ID: "compare", Tool: "reconcile.compare",
					Args: map[string]any{"invoices": "${steps.invoices.rows}", "ledger": "${steps.ledger.rows}"}},
				{ID: "email", Tool: "email.draft",
					Args:   map[string]any{"to": "accounts@supplier.example", "mismatches": "${steps.compare.mismatches}"},
					Accept: []runtime.Check{{Path: "body", Op: "contains", Value: "INV-"}}},
			},
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("job %s (journal in %s)\n", id, *dir)
	}
	res, err := engine.Run(ctx, id)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n", res)
}
