// Package planner turns a goal into steps with explicit inputs and acceptance tests, and
// revises a step the critic or the policy sent back.
package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/critic"
)

// Step is one unit of work: a tool, its arguments and the test its result must pass.
// String arguments of the form "${steps.<id>.<path>}" are filled from earlier results.
type Step struct {
	ID           string         `json:"id"`
	Tool         string         `json:"tool"`
	Args         map[string]any `json:"args,omitempty"`
	Accept       []critic.Check `json:"accept,omitempty"`
	Alternatives []Step         `json:"alternatives,omitempty"`
}

// Feedback is what the planner is told when a step comes back.
type Feedback struct {
	Step   Step            `json:"step"`
	Reason string          `json:"reason"`
	Failed *critic.Check   `json:"failed_check,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Return int             `json:"return"` // how many times this step has come back, starting at 1
}

// ErrGiveUp means the planner has no revision to offer.
var ErrGiveUp = errors.New("planner: no revision for this step")

type Planner interface {
	Plan(ctx context.Context, goal string, tools []string) ([]Step, error)
	Revise(ctx context.Context, goal string, fb Feedback) (Step, error)
}

// Static follows a plan written in advance. A returned step is replaced by its next
// alternative, if it has one.
type Static struct {
	Steps []Step
}

func (s Static) Plan(context.Context, string, []string) ([]Step, error) {
	if len(s.Steps) == 0 {
		return nil, errors.New("planner: empty plan")
	}
	return s.Steps, nil
}

func (s Static) Revise(_ context.Context, _ string, fb Feedback) (Step, error) {
	for _, st := range s.Steps {
		if st.ID == fb.Step.ID && fb.Return <= len(st.Alternatives) {
			alt := st.Alternatives[fb.Return-1]
			alt.ID = st.ID
			return alt, nil
		}
	}
	return Step{}, ErrGiveUp
}

var ref = regexp.MustCompile(`^\$\{steps\.([A-Za-z0-9_-]+)(?:\.(.+))?\}$`)

// Resolve fills "${steps.id.path}" references from earlier step results.
func Resolve(args map[string]any, results map[string]json.RawMessage) (map[string]any, error) {
	out := make(map[string]any, len(args))
	for k, v := range args {
		r, err := resolveValue(v, results)
		if err != nil {
			return nil, fmt.Errorf("argument %s: %w", k, err)
		}
		out[k] = r
	}
	return out, nil
}

// Sources lists the steps whose results a value refers to, in the order they appear.
func Sources(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		if m := ref.FindStringSubmatch(x); m != nil {
			out = append(out, m[1])
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, Sources(x[k])...)
		}
	case []any:
		for _, it := range x {
			out = append(out, Sources(it)...)
		}
	}
	return out
}

func resolveValue(v any, results map[string]json.RawMessage) (any, error) {
	switch x := v.(type) {
	case string:
		m := ref.FindStringSubmatch(x)
		if m == nil {
			return x, nil
		}
		raw, ok := results[m[1]]
		if !ok {
			return nil, fmt.Errorf("step %s has no result yet", m[1])
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, err
		}
		got, ok := critic.Lookup(decoded, m[2])
		if !ok {
			return nil, fmt.Errorf("%s not found in the result of %s", m[2], m[1])
		}
		return got, nil
	case map[string]any:
		return Resolve(x, results)
	case []any:
		out := make([]any, len(x))
		for i, it := range x {
			r, err := resolveValue(it, results)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	}
	return v, nil
}

// Chat plans with a model behind an OpenAI-compatible endpoint, asking for the steps as JSON.
type Chat struct {
	Endpoint string
	Model    string
	Client   *http.Client
}

func (c Chat) ask(ctx context.Context, prompt string, out any) error {
	body, _ := json.Marshal(map[string]any{
		"model": c.Model, "temperature": 0,
		"response_format": map[string]string{"type": "json_object"},
		"messages":        []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Endpoint, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("planner model: %s", resp.Status)
	}
	var reply struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil || len(reply.Choices) == 0 {
		return fmt.Errorf("planner model: bad reply")
	}
	return json.Unmarshal([]byte(reply.Choices[0].Message.Content), out)
}

func (c Chat) Plan(ctx context.Context, goal string, tools []string) ([]Step, error) {
	var out struct {
		Steps []Step `json:"steps"`
	}
	prompt := fmt.Sprintf("Goal: %s\nTools: %s\nReply with JSON {\"steps\": [{\"id\", \"tool\", \"args\", "+
		"\"accept\": [{\"path\", \"op\", \"value\"}]}]}. Refer to earlier results as \"${steps.<id>.<path>}\".",
		goal, strings.Join(tools, ", "))
	if err := c.ask(ctx, prompt, &out); err != nil {
		return nil, err
	}
	return out.Steps, nil
}

func (c Chat) Revise(ctx context.Context, goal string, fb Feedback) (Step, error) {
	b, _ := json.Marshal(fb)
	var out Step
	prompt := fmt.Sprintf("Goal: %s\nThis step came back with the failure attached:\n%s\n"+
		"Reply with the revised step as JSON, keeping its id.", goal, b)
	if err := c.ask(ctx, prompt, &out); err != nil {
		return Step{}, err
	}
	out.ID = fb.Step.ID
	return out, nil
}
