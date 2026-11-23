package jiratest

import (
	"fmt"
	"strconv"
	"strings"
)

// jqlTokKind classifies JQL tokens.
type jqlTokKind int

const (
	tokEOF      jqlTokKind = iota
	tokIdent               // words: fields, keywords, function names
	tokNumber              // digits only
	tokString              // "quoted" (text holds the unquoted value)
	tokDuration            // -Nd (num holds N)
	tokLParen
	tokRParen
	tokComma
	tokEq
	tokGte
)

type jqlToken struct {
	kind jqlTokKind
	text string // value (unquoted for strings, digits for numbers)
	raw  string // source text, for messages
	num  int    // duration days
	pos  int    // byte offset in the query
}

// jqlValue is one entry of an IN list: a number or a string.
type jqlValue struct {
	text   string
	quoted bool
}

func jqlError(pos int, format string, args ...any) error {
	return fmt.Errorf("jql: %s (position %d)", fmt.Sprintf(format, args...), pos+1)
}

func isWordChar(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// lexJQL splits s into tokens. Anything outside the subset's alphabet is an error.
func lexJQL(s string) ([]jqlToken, error) {
	var out []jqlToken
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(' || c == ')' || c == ',' || c == '=':
			kind := map[byte]jqlTokKind{'(': tokLParen, ')': tokRParen, ',': tokComma, '=': tokEq}[c]
			out = append(out, jqlToken{kind: kind, text: string(c), raw: string(c), pos: i})
			i++
		case c == '>':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, jqlToken{kind: tokGte, text: ">=", raw: ">=", pos: i})
				i += 2
				continue
			}
			return nil, jqlError(i, "expected '>='")
		case c == '"':
			j := strings.IndexByte(s[i+1:], '"')
			if j < 0 {
				return nil, jqlError(i, "unterminated string")
			}
			out = append(out, jqlToken{kind: tokString, text: s[i+1 : i+1+j], raw: s[i : i+2+j], pos: i})
			i += j + 2
		case c == '-':
			j := i + 1
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			if j == i+1 || j >= len(s) || s[j] != 'd' || (j+1 < len(s) && isWordChar(s[j+1])) {
				return nil, jqlError(i, "expected a relative duration such as -15d")
			}
			n, err := strconv.Atoi(s[i+1 : j])
			if err != nil {
				return nil, jqlError(i, "duration is too large")
			}
			out = append(out, jqlToken{kind: tokDuration, text: s[i+1 : j], raw: s[i : j+1], num: n, pos: i})
			i = j + 1
		case isWordChar(c):
			j := i
			for j < len(s) && isWordChar(s[j]) {
				j++
			}
			word := s[i:j]
			kind := tokIdent
			if isDigits(word) {
				kind = tokNumber
			}
			out = append(out, jqlToken{kind: kind, text: word, raw: word, pos: i})
			i = j
		default:
			return nil, jqlError(i, "unexpected character %q", c)
		}
	}
	return append(out, jqlToken{kind: tokEOF, pos: len(s)}), nil
}

// jqlParser is a recursive-descent parser: or → and → not → primary → clause.
type jqlParser struct {
	toks []jqlToken
	i    int
}

func (p *jqlParser) peek() jqlToken { return p.toks[p.i] }

func (p *jqlParser) advance() jqlToken {
	t := p.toks[p.i]
	if t.kind != tokEOF {
		p.i++
	}
	return t
}

func (p *jqlParser) acceptKeyword(kw string) bool {
	t := p.peek()
	if t.kind == tokIdent && strings.EqualFold(t.text, kw) {
		p.advance()
		return true
	}
	return false
}

func (p *jqlParser) unexpected() error {
	t := p.peek()
	if t.kind == tokEOF {
		return jqlError(t.pos, "unexpected end of query")
	}
	return jqlError(t.pos, "unexpected %s", t.raw)
}

func (p *jqlParser) expect(kind jqlTokKind, what string) (jqlToken, error) {
	t := p.peek()
	if t.kind != kind {
		return t, p.unexpected0(what)
	}
	return p.advance(), nil
}

func (p *jqlParser) unexpected0(what string) error {
	t := p.peek()
	if t.kind == tokEOF {
		return jqlError(t.pos, "expected %s at the end of the query", what)
	}
	return jqlError(t.pos, "expected %s, found %s", what, t.raw)
}

func (p *jqlParser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.acceptKeyword("OR") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orNode{left, right}
	}
	return left, nil
}

func (p *jqlParser) parseAnd() (node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.acceptKeyword("AND") {
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = andNode{left, right}
	}
	return left, nil
}

func (p *jqlParser) parseNot() (node, error) {
	if p.acceptKeyword("NOT") {
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return notNode{x}, nil
	}
	return p.parsePrimary()
}

func (p *jqlParser) parsePrimary() (node, error) {
	if p.peek().kind != tokLParen {
		return p.parseClause()
	}
	p.advance()
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokRParen, "')'"); err != nil {
		return nil, err
	}
	return e, nil
}

func (p *jqlParser) parseClause() (node, error) {
	field := p.peek()
	if field.kind != tokIdent {
		return nil, p.unexpected()
	}
	p.advance()
	switch strings.ToLower(field.text) {
	case "filter":
		if _, err := p.expect(tokEq, "'='"); err != nil {
			return nil, err
		}
		t, err := p.expect(tokNumber, "a filter id")
		if err != nil {
			return nil, err
		}
		id, err := strconv.Atoi(t.text)
		if err != nil {
			return nil, jqlError(t.pos, "filter id %s is too large", t.text)
		}
		return filterNode{id}, nil
	case "sprint":
		return p.parseSprintClause()
	case "status":
		if !p.acceptKeyword("IN") {
			return nil, p.unexpected0("IN after status")
		}
		vals, err := p.parseInList(true)
		if err != nil {
			return nil, err
		}
		return statusInNode{vals}, nil
	case "id":
		if !p.acceptKeyword("IN") {
			return nil, p.unexpected0("IN after id")
		}
		vals, err := p.parseInList(true)
		if err != nil {
			return nil, err
		}
		return idInNode{vals}, nil
	case "updated":
		if _, err := p.expect(tokGte, "'>='"); err != nil {
			return nil, err
		}
		d, err := p.expect(tokDuration, "a relative duration such as -15d")
		if err != nil {
			return nil, err
		}
		return updatedNode{d.num}, nil
	default:
		return nil, jqlError(field.pos, "field %s is not supported", field.raw)
	}
}

func (p *jqlParser) parseSprintClause() (node, error) {
	switch {
	case p.acceptKeyword("IS"):
		if !p.acceptKeyword("EMPTY") {
			return nil, p.unexpected0("EMPTY")
		}
		return sprintEmptyNode{}, nil
	case p.acceptKeyword("NOT"):
		if !p.acceptKeyword("IN") {
			return nil, p.unexpected0("IN")
		}
		if !p.acceptKeyword("openSprints") {
			return nil, p.unexpected0("openSprints()")
		}
		if err := p.parseOpenSprints(); err != nil {
			return nil, err
		}
		return notNode{openSprintsNode{}}, nil
	case p.acceptKeyword("IN"):
		if t := p.peek(); t.kind == tokIdent && strings.EqualFold(t.text, "openSprints") {
			p.advance()
			if err := p.parseOpenSprints(); err != nil {
				return nil, err
			}
			return openSprintsNode{}, nil
		}
		vals, err := p.parseInList(false)
		if err != nil {
			return nil, err
		}
		ids := make([]int, 0, len(vals))
		for _, v := range vals {
			id, err := strconv.Atoi(v.text)
			if err != nil {
				return nil, jqlError(p.peek().pos, "sprint id %s is too large", v.text)
			}
			ids = append(ids, id)
		}
		return sprintInNode{ids}, nil
	default:
		return nil, p.unexpected0("IS, NOT or IN after sprint")
	}
}

// parseOpenSprints consumes the "()" that follows openSprints.
func (p *jqlParser) parseOpenSprints() error {
	if _, err := p.expect(tokLParen, "'('"); err != nil {
		return err
	}
	_, err := p.expect(tokRParen, "')'")
	return err
}

// parseInList parses "( value, … )". Numbers are always allowed; quoted strings only when allowQuoted.
func (p *jqlParser) parseInList(allowQuoted bool) ([]jqlValue, error) {
	if _, err := p.expect(tokLParen, "'('"); err != nil {
		return nil, err
	}
	var vals []jqlValue
	for {
		t := p.peek()
		switch {
		case t.kind == tokNumber:
			p.advance()
			vals = append(vals, jqlValue{text: t.text})
		case t.kind == tokString && allowQuoted:
			p.advance()
			vals = append(vals, jqlValue{text: t.text, quoted: true})
		default:
			return nil, p.unexpected0("a value")
		}
		if p.peek().kind != tokComma {
			break
		}
		p.advance()
	}
	if _, err := p.expect(tokRParen, "')'"); err != nil {
		return nil, err
	}
	return vals, nil
}
