package decide

import (
	"fmt"
	"strconv"
	"strings"
)

// Effect is what a policy does when it matches.
type Effect int

const (
	EffectPermit Effect = iota
	EffectForbid
)

// Policy is one compiled Cedar statement.
type Policy struct {
	Effect      Effect
	Annotations map[string]string
	Principal   string   // "" matches any principal
	Actions     []string // nil matches any action
	When        []Expr
	Unless      []Expr
	Source      string // file:line
}

// Name is how decisions refer to the policy: @id, else @hold, else its position.
func (p *Policy) Name() string {
	if v, ok := p.Annotations["id"]; ok {
		return v
	}
	if v, ok := p.Annotations["hold"]; ok {
		return v
	}
	return p.Source
}

// Expr is a node of a when/unless condition.
type Expr interface{}

type (
	litExpr struct{ v any }
	refExpr struct{ path []string }
	setExpr struct{ items []Expr }
	notExpr struct{ x Expr }
	binExpr struct {
		op   string
		l, r Expr
	}
)

type parser struct {
	toks []token
	pos  int
	file string
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) expect(text string) error {
	t := p.next()
	if t.text != text || t.kind == tString {
		return fmt.Errorf("%s:%d: expected %q, found %q", p.file, t.line, text, t.text)
	}
	return nil
}

func (p *parser) accept(text string) bool {
	if t := p.peek(); t.text == text && t.kind != tString {
		p.pos++
		return true
	}
	return false
}

// Parse compiles the Cedar subset the engine supports: permit/forbid statements with
// annotations, a scope on principal and action, and when/unless conditions over context,
// resource and principal attributes.
func Parse(file, src string) ([]*Policy, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	p := &parser{toks: toks, file: file}
	var out []*Policy
	for p.peek().kind != tEOF {
		pol, err := p.policy()
		if err != nil {
			return nil, err
		}
		out = append(out, pol)
	}
	return out, nil
}

func (p *parser) policy() (*Policy, error) {
	pol := &Policy{Annotations: map[string]string{}}
	for p.accept("@") {
		name := p.next().text
		if err := p.expect("("); err != nil {
			return nil, err
		}
		v := p.next()
		if v.kind != tString {
			return nil, fmt.Errorf("%s:%d: annotation value must be a string", p.file, v.line)
		}
		pol.Annotations[name] = v.text
		if err := p.expect(")"); err != nil {
			return nil, err
		}
	}
	head := p.next()
	pol.Source = fmt.Sprintf("%s:%d", p.file, head.line)
	switch head.text {
	case "permit":
		pol.Effect = EffectPermit
	case "forbid":
		pol.Effect = EffectForbid
	default:
		return nil, fmt.Errorf("%s:%d: expected permit or forbid, found %q", p.file, head.line, head.text)
	}
	if err := p.scope(pol); err != nil {
		return nil, err
	}
	for {
		switch {
		case p.accept("when"):
			e, err := p.block()
			if err != nil {
				return nil, err
			}
			pol.When = append(pol.When, e)
			continue
		case p.accept("unless"):
			e, err := p.block()
			if err != nil {
				return nil, err
			}
			pol.Unless = append(pol.Unless, e)
			continue
		}
		break
	}
	return pol, p.expect(";")
}

func (p *parser) entity() (string, error) {
	typ := p.next().text
	for p.accept("::") {
		t := p.next()
		if t.kind == tString {
			return typ + "::" + strconv.Quote(t.text), nil
		}
		typ += "::" + t.text
	}
	return "", fmt.Errorf("%s:%d: expected an entity such as Action::\"name\"", p.file, p.peek().line)
}

func (p *parser) scope(pol *Policy) error {
	if err := p.expect("("); err != nil {
		return err
	}
	if err := p.expect("principal"); err != nil {
		return err
	}
	if p.accept("==") {
		e, err := p.entity()
		if err != nil {
			return err
		}
		pol.Principal = e
	}
	if err := p.expect(","); err != nil {
		return err
	}
	if err := p.expect("action"); err != nil {
		return err
	}
	switch {
	case p.accept("=="):
		e, err := p.entity()
		if err != nil {
			return err
		}
		pol.Actions = []string{actionName(e)}
	case p.accept("in"):
		if err := p.expect("["); err != nil {
			return err
		}
		for !p.accept("]") {
			e, err := p.entity()
			if err != nil {
				return err
			}
			pol.Actions = append(pol.Actions, actionName(e))
			p.accept(",")
		}
	}
	if err := p.expect(","); err != nil {
		return err
	}
	if err := p.expect("resource"); err != nil {
		return err
	}
	return p.expect(")")
}

func actionName(entity string) string {
	i := strings.Index(entity, "::")
	s, _ := strconv.Unquote(entity[i+2:])
	return s
}

func (p *parser) block() (Expr, error) {
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	e, err := p.or()
	if err != nil {
		return nil, err
	}
	return e, p.expect("}")
}

func (p *parser) or() (Expr, error) {
	l, err := p.and()
	for err == nil && p.accept("||") {
		var r Expr
		r, err = p.and()
		l = binExpr{"||", l, r}
	}
	return l, err
}

func (p *parser) and() (Expr, error) {
	l, err := p.cmp()
	for err == nil && p.accept("&&") {
		var r Expr
		r, err = p.cmp()
		l = binExpr{"&&", l, r}
	}
	return l, err
}

func (p *parser) cmp() (Expr, error) {
	l, err := p.unary()
	if err != nil {
		return nil, err
	}
	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<", "in", "like", "contains"} {
		if p.accept(op) {
			r, err := p.unary()
			return binExpr{op, l, r}, err
		}
	}
	return l, nil
}

func (p *parser) unary() (Expr, error) {
	if p.accept("!") {
		x, err := p.unary()
		return notExpr{x}, err
	}
	return p.primary()
}

func (p *parser) primary() (Expr, error) {
	t := p.next()
	switch {
	case t.kind == tString:
		return litExpr{t.text}, nil
	case t.kind == tNumber:
		f, err := strconv.ParseFloat(t.text, 64)
		return litExpr{f}, err
	case t.text == "true" || t.text == "false":
		return litExpr{t.text == "true"}, nil
	case t.text == "(":
		e, err := p.or()
		if err != nil {
			return nil, err
		}
		return e, p.expect(")")
	case t.text == "[":
		var items []Expr
		for !p.accept("]") {
			e, err := p.or()
			if err != nil {
				return nil, err
			}
			items = append(items, e)
			p.accept(",")
		}
		return setExpr{items}, nil
	case t.kind == tIdent && (t.text == "context" || t.text == "resource" || t.text == "principal"):
		path := []string{t.text}
		for p.accept(".") {
			path = append(path, p.next().text)
		}
		return refExpr{path}, nil
	}
	return nil, fmt.Errorf("%s:%d: unexpected %q in condition", p.file, t.line, t.text)
}
