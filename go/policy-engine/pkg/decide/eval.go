package decide

import (
	"fmt"
	"path"
	"strings"
)

type env struct {
	context   map[string]any
	resource  map[string]any
	principal map[string]any
}

func (e env) lookup(p []string) (any, error) {
	var cur any
	switch p[0] {
	case "context":
		cur = e.context
	case "resource":
		cur = e.resource
	case "principal":
		cur = e.principal
	}
	for _, k := range p[1:] {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s is not a record", strings.Join(p, "."))
		}
		if cur, ok = m[k]; !ok {
			return nil, fmt.Errorf("%s is missing", strings.Join(p, "."))
		}
	}
	return normalise(cur), nil
}

func normalise(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	}
	return v
}

// eval evaluates a condition. A missing attribute is an error, and a policy whose condition
// errors does not match, as in Cedar.
func eval(x Expr, e env) (any, error) {
	switch n := x.(type) {
	case litExpr:
		return n.v, nil
	case refExpr:
		return e.lookup(n.path)
	case setExpr:
		out := make([]any, 0, len(n.items))
		for _, it := range n.items {
			v, err := eval(it, e)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case notExpr:
		v, err := eval(n.x, e)
		if err != nil {
			return nil, err
		}
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("! needs a boolean")
		}
		return !b, nil
	case binExpr:
		if n.op == "&&" || n.op == "||" {
			l, err := evalBool(n.l, e)
			if err != nil {
				return nil, err
			}
			if (n.op == "&&" && !l) || (n.op == "||" && l) {
				return l, nil
			}
			return evalBool(n.r, e)
		}
		l, err := eval(n.l, e)
		if err != nil {
			return nil, err
		}
		r, err := eval(n.r, e)
		if err != nil {
			return nil, err
		}
		return compare(n.op, l, r)
	}
	return nil, fmt.Errorf("unknown expression %T", x)
}

func evalBool(x Expr, e env) (bool, error) {
	v, err := eval(x, e)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("condition is not a boolean")
	}
	return b, nil
}

func equal(a, b any) bool {
	switch x := a.(type) {
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		return false
	}
	return a == b
}

func compare(op string, l, r any) (any, error) {
	switch op {
	case "==":
		return equal(l, r), nil
	case "!=":
		return !equal(l, r), nil
	case "in":
		if set, ok := r.([]any); ok {
			for _, it := range set {
				if equal(l, it) {
					return true, nil
				}
			}
			return false, nil
		}
		return equal(l, r), nil
	case "contains":
		return compare("in", r, l)
	case "like":
		s, ok1 := l.(string)
		pat, ok2 := r.(string)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("like needs strings")
		}
		m, err := path.Match(pat, s)
		return m, err
	}
	a, ok1 := l.(float64)
	b, ok2 := r.(float64)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("%s needs numbers, got %T and %T", op, l, r)
	}
	switch op {
	case ">":
		return a > b, nil
	case "<":
		return a < b, nil
	case ">=":
		return a >= b, nil
	case "<=":
		return a <= b, nil
	}
	return nil, fmt.Errorf("unknown operator %s", op)
}
