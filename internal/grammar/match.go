package grammar

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/lexer"
)

// failed marks a memo entry as a non-match. Positions are non-negative,
// so -1 is unambiguous.
const failed = -1

type matcher struct {
	g    *Grammar
	toks []lexer.Token
	memo []map[int]int // memo[ruleIndex][pos] = end position, or failed
	used []bool        // rules that matched at least once

	// furthest is the deepest token index any attempt reached. On
	// failure it is far more useful than the start position: it points
	// at the token the grammar could not get past, which is where the
	// missing production is.
	furthest int
}

// Match reports whether the whole token stream is derivable from the
// start rule. On failure it returns the token index the grammar reached
// before giving up.
func (g *Grammar) Match(toks []lexer.Token) (ok bool, stuckAt int) {
	ok, stuck, _ := g.MatchCoverage(toks)
	return ok, stuck
}

// MatchCoverage is Match, plus the set of rules that matched at least
// once. Accumulated across a corpus it answers "which productions does
// no real program exercise?" — which for a normative grammar is the
// question that separates a described language from an invented one.
//
// Coverage is only reported for a SUCCESSFUL match, so a rule credited
// here was reached on a path that produced a whole valid program. A rule
// can still be over-credited by a branch that matched and was then
// backtracked past; that errs toward not flagging, which is the safe
// direction for a gate that fails the build.
func (g *Grammar) MatchCoverage(toks []lexer.Token) (ok bool, stuckAt int, used []string) {
	m := &matcher{g: g, toks: toks, memo: make([]map[int]int, len(g.rules)), used: make([]bool, len(g.rules))}
	end := m.matchNode(&node{kind: nRef, text: g.names[g.start], ruleI: g.start}, 0)
	if end == failed {
		return false, m.furthest, nil
	}
	// Trailing EOF token is not consumed by the grammar itself.
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
		// Ordered choice: first match wins, as in recursive descent.
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
		// Greedy and possessive. A repetition that matched zero-width
		// would spin forever, so stop when no progress is made.
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
			// A contextual keyword is spelled as a literal where the
			// grammar needs it; everywhere else it is an ordinary name.
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
	// Left recursion would recurse forever. Seed the memo with a failure
	// so a rule that reaches itself at the same position terminates
	// instead of blowing the stack — the grammar is expected to be
	// non-left-recursive, and RuleIsLeftRecursive reports any that are.
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

// LeftRecursive returns the names of rules that can reach themselves
// without consuming a token. The matcher survives them (it seeds a
// failure), but they silently never match, so they are always a bug in
// the grammar rather than a style question.
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

// reachesLeft reports whether n can reach rule target in leading
// position without consuming input.
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
		// Only the leading item is in leading position, unless earlier
		// items can match empty — which nOpt and nRep both can.
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

// Unreachable returns rules the start symbol cannot reach — dead
// productions, which in a spec are worse than dead code: they read as
// normative and describe nothing.
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

// Context renders the tokens around a position, for a failure message
// that points at the construct the grammar could not derive.
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
