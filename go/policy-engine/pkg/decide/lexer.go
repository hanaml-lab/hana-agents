package decide

import (
	"fmt"
	"strings"
	"unicode"
)

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tString
	tNumber
	tPunct
)

type token struct {
	kind tokKind
	text string
	line int
}

func lex(src string) ([]token, error) {
	var out []token
	line := 1
	rs := []rune(src)
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case c == '\n':
			line++
			i++
		case unicode.IsSpace(c):
			i++
		case c == '/' && i+1 < len(rs) && rs[i+1] == '/':
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
		case c == '"':
			var b strings.Builder
			i++
			for i < len(rs) && rs[i] != '"' {
				if rs[i] == '\\' && i+1 < len(rs) {
					i++
				}
				b.WriteRune(rs[i])
				i++
			}
			if i >= len(rs) {
				return nil, fmt.Errorf("line %d: unterminated string", line)
			}
			i++
			out = append(out, token{tString, b.String(), line})
		case unicode.IsDigit(c) || (c == '-' && i+1 < len(rs) && unicode.IsDigit(rs[i+1])):
			j := i + 1
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == '.' || rs[j] == '_') {
				j++
			}
			out = append(out, token{tNumber, strings.ReplaceAll(string(rs[i:j]), "_", ""), line})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i + 1
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '_') {
				j++
			}
			out = append(out, token{tIdent, string(rs[i:j]), line})
			i = j
		default:
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			switch two {
			case "==", "!=", ">=", "<=", "&&", "||", "::":
				out = append(out, token{tPunct, two, line})
				i += 2
				continue
			}
			if strings.ContainsRune("(){}[],;.<>!@", c) {
				out = append(out, token{tPunct, string(c), line})
				i++
				continue
			}
			return nil, fmt.Errorf("line %d: unexpected %q", line, c)
		}
	}
	return append(out, token{tEOF, "", line}), nil
}
