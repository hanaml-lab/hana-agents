// Command agentdojo replays exported AgentDojo tool calls through the policy engine and counts
// how many injected attacker actions would have been stopped, and how many benign calls held
// or forbidden.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

type call struct {
	Function string         `json:"function"`
	Args     map[string]any `json:"args"`
}

type trace struct {
	Task         string   `json:"task"`
	Injection    bool     `json:"injection"`
	Calls        []call   `json:"calls"`
	AttackerGoal []string `json:"attacker_goal"`
	KnownPayees  []string `json:"known_payees"`
}

func main() {
	tracesPath := flag.String("traces", "sample-traces.jsonl", "exported traces")
	policies := flag.String("policies", "policies/", "policy directory")
	flag.Parse()
	engine, err := decide.Load(*policies)
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Open(*tracesPath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	var attacks, attacksThrough, benign, benignBlocked int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var t trace
		if err := json.Unmarshal(sc.Bytes(), &t); err != nil {
			log.Fatal(err)
		}
		goal := map[string]bool{}
		for _, g := range t.AttackerGoal {
			goal[g] = true
		}
		through, blocked := false, false
		for _, c := range t.Calls {
			ctx := map[string]any{"known_payees": t.KnownPayees}
			d := engine.Decide(context.Background(), decide.Request{Principal: "agent:banking", Action: c.Function, Args: c.Args, Context: ctx})
			if d.Outcome != decide.Allow {
				blocked = true
				continue
			}
			if t.Injection && goal[c.Function] {
				through = true
			}
		}
		if t.Injection {
			attacks++
			if through {
				attacksThrough++
			}
		} else {
			benign++
			if blocked {
				benignBlocked++
			}
		}
	}
	if err := sc.Err(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("policy version %s\n", engine.Version())
	fmt.Printf("injection runs: %d, attacker action allowed in %d (%.1f%%)\n", attacks, attacksThrough, pct(attacksThrough, attacks))
	fmt.Printf("benign runs: %d, with a call held or forbidden %d (%.1f%%)\n", benign, benignBlocked, pct(benignBlocked, benign))
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}
