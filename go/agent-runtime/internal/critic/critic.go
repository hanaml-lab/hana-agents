// Package critic checks each step's result against the step's acceptance test. A failed check
// sends the step back to the planner with the failure attached.
package critic

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Check is one acceptance condition on a step result. Path is a dot path into the result
// JSON ("" is the whole result; numeric segments index arrays).
type Check struct {
	Path  string `json:"path"`
	Op    string `json:"op"` // exists | nonempty | equals | gte | lte | contains
	Value any    `json:"value,omitempty"`
}

func (c Check) String() string {
	if c.Value == nil {
		return fmt.Sprintf("%s %s", c.Path, c.Op)
	}
	return fmt.Sprintf("%s %s %v", c.Path, c.Op, c.Value)
}

// Verdict is the critic's answer. Failed names the first check that did not hold.
type Verdict struct {
	OK     bool   `json:"ok"`
	Failed *Check `json:"failed,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Lookup walks a dot path through decoded JSON.
func Lookup(v any, path string) (any, bool) {
	if path == "" {
		return v, true
	}
	for _, seg := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			if v, ok = x[seg]; !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func holds(c Check, v any, found bool) (bool, string) {
	switch c.Op {
	case "exists":
		return found, "missing"
	case "nonempty":
		if !found || v == nil {
			return false, "missing"
		}
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.String, reflect.Slice, reflect.Map:
			return rv.Len() > 0, "empty"
		}
		return true, ""
	case "equals":
		a, aok := num(v)
		b, bok := num(c.Value)
		if aok && bok {
			return a == b, fmt.Sprintf("is %v", v)
		}
		return found && reflect.DeepEqual(v, c.Value), fmt.Sprintf("is %v", v)
	case "gte", "lte":
		a, aok := num(v)
		b, bok := num(c.Value)
		if !aok || !bok {
			return false, "not a number"
		}
		if c.Op == "gte" {
			return a >= b, fmt.Sprintf("is %v", a)
		}
		return a <= b, fmt.Sprintf("is %v", a)
	case "contains":
		switch x := v.(type) {
		case string:
			s, _ := c.Value.(string)
			return strings.Contains(x, s), "does not contain it"
		case []any:
			for _, it := range x {
				if reflect.DeepEqual(it, c.Value) {
					return true, ""
				}
			}
			return false, "does not contain it"
		}
		return false, "not a string or list"
	}
	return false, "unknown op " + c.Op
}

// Review applies the checks in order.
func Review(result json.RawMessage, checks []Check) Verdict {
	var v any
	if err := json.Unmarshal(result, &v); err != nil {
		return Verdict{false, nil, "result is not JSON: " + err.Error()}
	}
	for i := range checks {
		c := checks[i]
		got, found := Lookup(v, c.Path)
		if ok, why := holds(c, got, found); !ok {
			return Verdict{false, &c, fmt.Sprintf("check %q failed: %s", c.String(), why)}
		}
	}
	return Verdict{OK: true}
}
