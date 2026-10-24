package grammar

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/lexer"
)

const failed = -1

type matcher struct {
	g    *Grammar
	toks []lexer.Token
	memo []map[int]int
	used []bool

	furthest int
}

func (g *Grammar) Match(toks []lexer.Token) (ok bool, stuckAt int) {
	ok, stuck, _ := g.MatchCoverage(toks)
	return ok, stuck
}

func (g *Grammar) MatchCoverage(toks []lexer.Token) (ok bool, stuckAt int, used []string) {
	m := &matcher{g: g, toks: toks, memo: make([]map[int]int, len(g.rules)), used: make([]bool, len(g.rules))}
	end := m.matchNode(&node{kind: nRef, text: g.names[g.start], ruleI: g.start}, 0)
	if end == failed {
		return false, m.furthest, nil
	}
	for end < len(toks) && toks[end].Kind == lexer.EOF {
		end++
	}
	if end != len(toks) {
		return false, max(end, m.furthest), nil
	}
	for i, u := range m.used {
		if u {
			used = append(used, g.names[i])
		}
	}
	return true, 0, used
}

func (m *matcher) matchNode(n *node, pos int) int {
	if pos > m.furthest {
		m.furthest = pos
	}
	switch n.kind {
	case nRef:
		return m.matchRule(n.ruleI, pos)

	case nSeq:
		for _, k := range n.kids {
			pos = m.matchNode(k, pos)
			if pos == failed {
				return failed
			}
		}
		return pos

	case nAlt:
		for _, k := range n.kids {
			if end := m.matchNode(k, pos); end != failed {
				return end
			}
		}
		return failed

	case nOpt:
		if end := m.matchNode(n.kids[0], pos); end != failed {
			return end
		}
		return pos

	case nRep:
		for {
			end := m.matchNode(n.kids[0], pos)
			if end == failed || end == pos {
				return pos
			}
			pos = end
		}

	case nLit:
		t := m.tok(pos)
		if (t.Kind == lexer.Punct || t.Kind == lexer.Keyword || t.Kind == lexer.Ident) && t.Text == n.text {
			return pos + 1
		}
		return failed

	case nClass:
		t := m.tok(pos)
		switch n.text {
		case "IDENT":
			if t.Kind == lexer.Ident {
				return pos + 1
			}
		case "NUMBER":
			if t.Kind == lexer.Number {
				return pos + 1
			}
		case "FLOAT":
			if t.Kind == lexer.Float {
				return pos + 1
			}
		case "STRING":
			if t.Kind == lexer.String {
				return pos + 1
			}
		case "FSTRING":
			if t.Kind == lexer.FString {
				return pos + 1
			}
		case "CHAR":
			if t.Kind == lexer.Char {
				return pos + 1
			}
		case "BYTE":
			if t.Kind == lexer.Byte {
				return pos + 1
			}
		}
		return failed
	}
	panic(fmt.Sprintf("grammar: unknown node kind %d", n.kind))
}

func (m *matcher) matchRule(i, pos int) int {
	if m.memo[i] == nil {
		m.memo[i] = map[int]int{}
	}
	if end, seen := m.memo[i][pos]; seen {
		return end
	}
	m.memo[i][pos] = failed
	end := m.matchNode(m.g.rules[i], pos)
	m.memo[i][pos] = end
	if end != failed {
		m.used[i] = true
	}
	return end
}

func (m *matcher) tok(pos int) lexer.Token {
	if pos < len(m.toks) {
		return m.toks[pos]
	}
	return lexer.Token{Kind: lexer.EOF}
}

func (g *Grammar) LeftRecursive() []string {
	var bad []string
	for i, name := range g.names {
		seen := map[int]bool{}
		if g.reachesLeft(g.rules[i], i, seen) {
			bad = append(bad, name)
		}
	}
	return bad
}

func (g *Grammar) reachesLeft(n *node, target int, seen map[int]bool) bool {
	switch n.kind {
	case nRef:
		if n.ruleI == target {
			return true
		}
		if seen[n.ruleI] {
			return false
		}
		seen[n.ruleI] = true
		return g.reachesLeft(g.rules[n.ruleI], target, seen)
	case nSeq:
		for _, k := range n.kids {
			if g.reachesLeft(k, target, seen) {
				return true
			}
			if !nullable(k) {
				return false
			}
		}
		return false
	case nAlt:
		for _, k := range n.kids {
			if g.reachesLeft(k, target, seen) {
				return true
			}
		}
		return false
	case nOpt, nRep:
		return g.reachesLeft(n.kids[0], target, seen)
	}
	return false
}

func nullable(n *node) bool {
	switch n.kind {
	case nOpt, nRep:
		return true
	case nSeq:
		for _, k := range n.kids {
			if !nullable(k) {
				return false
			}
		}
		return true
	case nAlt:
		for _, k := range n.kids {
			if nullable(k) {
				return true
			}
		}
		return false
	}
	return false
}

func (g *Grammar) Unreachable() []string {
	seen := map[int]bool{g.start: true}
	var walk func(*node)
	walk = func(n *node) {
		if n.kind == nRef {
			if !seen[n.ruleI] {
				seen[n.ruleI] = true
				walk(g.rules[n.ruleI])
			}
			return
		}
		for _, k := range n.kids {
			walk(k)
		}
	}
	walk(g.rules[g.start])

	var dead []string
	for i, name := range g.names {
		if !seen[i] {
			dead = append(dead, name)
		}
	}
	return dead
}

func Context(toks []lexer.Token, at int) string {
	lo := max(0, at-6)
	hi := min(len(toks), at+6)
	var b strings.Builder
	for i := lo; i < hi; i++ {
		if i == at {
			b.WriteString(" >>> ")
		} else {
			b.WriteString(" ")
		}
		b.WriteString(toks[i].Text)
	}
	if at < len(toks) {
		fmt.Fprintf(&b, "   (line %d)", toks[at].Pos.Line)
	}
	return strings.TrimSpace(b.String())
}
