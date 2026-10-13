package runtime

import (
	"sort"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/planner"
)

// untrustedArgs names the arguments of a step whose values derive, through any chain of step
// references, from the output of a tool the job declared untrusted. The policy sees the list as
// context.untrusted_args, so a rule can hold or forbid an action whose arguments came from
// content the agent read (an email, a web page, an invoice) rather than from the plan.
//
// Taint follows data, not tools: a trusted tool called with an untrusted argument returns an
// untrusted result. It is computed from the journalled plan alone, so a replay sees the same list.
func untrustedArgs(s Step, plan []Step, untrusted []string) []any {
	out := []any{}
	if len(untrusted) == 0 {
		return out
	}
	bad := map[string]bool{}
	for _, t := range untrusted {
		bad[t] = true
	}
	byID := map[string]Step{}
	for _, p := range plan {
		byID[p.ID] = p
	}
	memo := map[string]bool{}
	var tainted func(id string, seen map[string]bool) bool
	tainted = func(id string, seen map[string]bool) bool {
		if v, ok := memo[id]; ok {
			return v
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		p, ok := byID[id]
		v := ok && (bad[p.Tool] || anyTainted(planner.Sources(map[string]any(p.Args)), tainted, seen))
		memo[id] = v
		return v
	}
	names := make([]string, 0, len(s.Args))
	for k := range s.Args {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if anyTainted(planner.Sources(s.Args[k]), tainted, map[string]bool{s.ID: true}) {
			out = append(out, k)
		}
	}
	return out
}

func anyTainted(ids []string, tainted func(string, map[string]bool) bool, seen map[string]bool) bool {
	for _, id := range ids {
		if tainted(id, seen) {
			return true
		}
	}
	return false
}
