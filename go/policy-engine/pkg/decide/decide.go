// Package decide answers allow, hold or forbid for a tool call an agent attempts.
//
// The strictest matching rule wins: forbid beats hold, hold beats permit, and a request no
// permit matches is forbidden. Every decision names the rule that made it.
package decide

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/capability"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/temporal"
)

// Outcome of a decision. The order is the order of strictness.
type Outcome int

const (
	Allow Outcome = iota
	Hold
	Forbid
)

func (o Outcome) String() string {
	return [...]string{"allow", "hold", "forbid"}[o]
}

// MarshalText makes outcomes read as words in JSON.
func (o Outcome) MarshalText() ([]byte, error) { return []byte(o.String()), nil }

// Request is one attempted tool call.
type Request struct {
	Principal  string            `json:"principal"`
	Action     string            `json:"action"`
	Args       map[string]any    `json:"args"`
	Context    map[string]any    `json:"context,omitempty"`
	Attributes map[string]any    `json:"principal_attributes,omitempty"`
	Token      *capability.Token `json:"-"`
	Now        time.Time         `json:"now,omitempty"`
	JobStarted time.Time         `json:"job_started,omitempty"`
}

// Decision is the answer and the rule it came from.
type Decision struct {
	Outcome Outcome `json:"outcome"`
	Rule    string  `json:"rule"`
	Version string  `json:"policy_version"`
	Reason  string  `json:"reason,omitempty"`
}

// Set is a compiled set of policies with a content hash as its version.
type Set struct {
	Policies []*Policy
	Version  string
	windows  map[*Policy]temporal.Window
}

// Compile builds a set from named sources (file name to Cedar text).
func Compile(sources map[string]string) (*Set, error) {
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	set := &Set{windows: map[*Policy]temporal.Window{}}
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte(sources[n]))
		ps, err := Parse(n, sources[n])
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if w, ok := p.Annotations["window"]; ok {
				win, err := temporal.Parse(w)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", p.Source, err)
				}
				set.windows[p] = win
			}
		}
		set.Policies = append(set.Policies, ps...)
	}
	set.Version = hex.EncodeToString(h.Sum(nil))[:12]
	return set, nil
}

// Engine decides requests against a policy set. It is safe for concurrent use.
type Engine struct {
	set *Set
}

// Load compiles every .cedar file under dir.
func Load(dir string) (*Engine, error) {
	sources := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".cedar") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		sources[rel] = string(b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no .cedar files under %s", dir)
	}
	set, err := Compile(sources)
	if err != nil {
		return nil, err
	}
	return &Engine{set: set}, nil
}

// New wraps an already compiled set.
func New(set *Set) *Engine { return &Engine{set: set} }

// Version identifies the policy text the engine decides with.
func (e *Engine) Version() string { return e.set.Version }

func principalMatches(scope, principal string) bool {
	if scope == "" {
		return true
	}
	// Agent::"ap-clerk" matches "agent:ap-clerk"
	i := strings.Index(scope, "::")
	typ := strings.ToLower(scope[:i])
	id := strings.Trim(scope[i+2:], `"`)
	return principal == typ+":"+id
}

func (s *Set) matches(p *Policy, r Request, ev env) bool {
	if !principalMatches(p.Principal, r.Principal) {
		return false
	}
	if p.Actions != nil {
		found := false
		for _, a := range p.Actions {
			if a == r.Action {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if w, ok := s.windows[p]; ok && !w.Contains(r.Now, r.JobStarted) {
		return false
	}
	for _, c := range p.When {
		if ok, err := evalBool(c, ev); err != nil || !ok {
			return false
		}
	}
	for _, c := range p.Unless {
		if ok, err := evalBool(c, ev); err != nil || ok {
			return false
		}
	}
	return true
}

// Decide checks the request's capability token, then the policies.
func (e *Engine) Decide(_ context.Context, r Request) Decision {
	if r.Now.IsZero() {
		r.Now = time.Now()
	}
	if r.Token != nil {
		if err := r.Token.Allows(r.Action, resourceOf(r.Args), r.Now); err != nil {
			return Decision{Forbid, "capability", e.set.Version, err.Error()}
		}
	}
	ctx := map[string]any{}
	for k, v := range r.Args {
		ctx[k] = v
	}
	for k, v := range r.Context {
		ctx[k] = v
	}
	ev := env{context: ctx, resource: r.Args, principal: r.Attributes}
	best := Decision{Forbid, "default-deny", e.set.Version, "no policy permits this action"}
	matched := false
	for _, p := range e.set.Policies {
		if !e.set.matches(p, r, ev) {
			continue
		}
		var d Decision
		switch {
		case p.Effect == EffectForbid:
			d = Decision{Forbid, p.Name(), e.set.Version, ""}
		case p.Annotations["hold"] != "":
			d = Decision{Hold, p.Name(), e.set.Version, ""}
		default:
			d = Decision{Allow, p.Name(), e.set.Version, ""}
		}
		if !matched || d.Outcome > best.Outcome {
			best, matched = d, true
		}
	}
	return best
}

func resourceOf(args map[string]any) string {
	for _, k := range []string{"resource", "payee", "account", "url", "path"} {
		if v, ok := args[k].(string); ok {
			return v
		}
	}
	return ""
}

// Tenants caches one compiled engine per tenant, rebuilt only when the policy text changes.
type Tenants struct {
	mu      sync.RWMutex
	engines map[string]*Engine
}

func NewTenants() *Tenants { return &Tenants{engines: map[string]*Engine{}} }

// Get returns the tenant's engine, compiling sources only if their version is new.
func (t *Tenants) Get(tenant string, sources map[string]string) (*Engine, error) {
	set, err := Compile(sources)
	if err != nil {
		return nil, err
	}
	t.mu.RLock()
	cur, ok := t.engines[tenant]
	t.mu.RUnlock()
	if ok && cur.Version() == set.Version {
		return cur, nil
	}
	e := New(set)
	t.mu.Lock()
	t.engines[tenant] = e
	t.mu.Unlock()
	return e, nil
}
