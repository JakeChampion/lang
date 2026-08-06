// Package grammar reads Fern's syntactic grammar (spec/grammar.ebnf) and
// matches token streams against it.
//
// The grammar is a PEG, not a context-free grammar, and that is a
// deliberate choice rather than an implementation shortcut: the artefact
// it describes is a hand-written recursive-descent parser, and PEG is
// the formalism that actually models one. Alternation is ORDERED — the
// first branch that matches wins, exactly as a chain of `if p.match(...)`
// does — and repetition is greedy and possessive, exactly as a `for`
// loop over `p.accept(...)` is. A CFG would describe a parser nobody
// wrote, and its ambiguities would have to be resolved by prose.
//
// The consequence to keep in mind when editing spec/grammar.ebnf: a rule
// like `{ X } X` can never match, because the repetition consumes every
// X and does not give one back. Order alternatives longest-first.
package grammar

import (
	"fmt"
	"sort"
	"strings"
)

// Node kinds in a parsed grammar expression.
type nodeKind int

const (
	nSeq   nodeKind = iota // a b c
	nAlt                   // a | b | c   (ordered)
	nRep                   // { a }       (greedy, possessive)
	nOpt                   // [ a ]
	nRef                   // RuleName
	nLit                   // 'text'      keyword or punctuation
	nClass                 // IDENT, NUMBER, FLOAT, STRING, FSTRING
)

type node struct {
	kind  nodeKind
	kids  []*node
	text  string // nLit, nRef, nClass
	ruleI int    // nRef: resolved rule index, -1 until linked
}

// Grammar is a linked set of rules ready to match against.
type Grammar struct {
	names []string
	rules []*node
	index map[string]int
	start int
}

// RuleNames returns every rule name, sorted — for coverage reporting.
func (g *Grammar) RuleNames() []string {
	out := append([]string(nil), g.names...)
	sort.Strings(out)
	return out
}

// tokenClasses are the terminals produced by the lexical grammar. Every
// other terminal is a literal keyword or punctuator.
var tokenClasses = map[string]bool{
	"IDENT": true, "NUMBER": true, "FLOAT": true, "STRING": true, "FSTRING": true,
}

// Parse reads grammar source: a sequence of `Name = expr ;` rules, with
// `#` comments. The first rule is the start symbol.
func Parse(src string) (*Grammar, error) {
	g := &Grammar{index: map[string]int{}}

	for _, chunk := range splitRules(src) {
		name, body, ok := strings.Cut(chunk, "=")
		if !ok {
			return nil, fmt.Errorf("rule %q: missing `=`", clip(chunk))
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("rule %q: empty name", clip(chunk))
		}
		if _, dup := g.index[name]; dup {
			return nil, fmt.Errorf("rule %q: defined twice", name)
		}
		p := &gparser{toks: lexGrammar(body)}
		expr, err := p.parseAlt()
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", name, err)
		}
		if p.pos != len(p.toks) {
			return nil, fmt.Errorf("rule %s: trailing input at %q", name, p.toks[p.pos])
		}
		g.index[name] = len(g.rules)
		g.names = append(g.names, name)
		g.rules = append(g.rules, expr)
	}

	if len(g.rules) == 0 {
		return nil, fmt.Errorf("grammar is empty")
	}
	if err := g.link(); err != nil {
		return nil, err
	}
	return g, nil
}

// link resolves every nRef to a rule index, so matching never does a map
// lookup, and reports references to rules that do not exist — the most
// common way to break a grammar file.
func (g *Grammar) link() error {
	var missing []string
	var walk func(*node)
	walk = func(n *node) {
		if n.kind == nRef {
			i, ok := g.index[n.text]
			if !ok {
				missing = append(missing, n.text)
				return
			}
			n.ruleI = i
		}
		for _, k := range n.kids {
			walk(k)
		}
	}
	for _, r := range g.rules {
		walk(r)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("undefined rule(s): %s", strings.Join(uniq(missing), ", "))
	}
	return nil
}

// splitRules cuts the source into `...;` chunks, dropping comments. A
// `;` inside a quoted terminal (the language has one, `';'`) does not
// end a rule.
func splitRules(src string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, line := range strings.Split(src, "\n") {
		if i := commentStart(line); i >= 0 {
			line = line[:i]
		}
		for _, r := range line {
			switch {
			case r == '\'':
				inQuote = !inQuote
				cur.WriteRune(r)
			case r == ';' && !inQuote:
				if s := strings.TrimSpace(cur.String()); s != "" {
					out = append(out, s)
				}
				cur.Reset()
			default:
				cur.WriteRune(r)
			}
		}
		cur.WriteByte('\n')
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// commentStart finds a `#` that is not inside a quoted terminal.
func commentStart(line string) int {
	inQuote := false
	for i, r := range line {
		switch r {
		case '\'':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return i
			}
		}
	}
	return -1
}

// --- the grammar-file's own tiny parser ------------------------------------

type gparser struct {
	toks []string
	pos  int
}

func (p *gparser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *gparser) parseAlt() (*node, error) {
	first, err := p.parseSeq()
	if err != nil {
		return nil, err
	}
	if p.peek() != "|" {
		return first, nil
	}
	alt := &node{kind: nAlt, kids: []*node{first}}
	for p.peek() == "|" {
		p.pos++
		next, err := p.parseSeq()
		if err != nil {
			return nil, err
		}
		alt.kids = append(alt.kids, next)
	}
	return alt, nil
}

func (p *gparser) parseSeq() (*node, error) {
	var kids []*node
	for {
		t := p.peek()
		if t == "" || t == "|" || t == ")" || t == "}" || t == "]" {
			break
		}
		item, err := p.parseItem()
		if err != nil {
			return nil, err
		}
		kids = append(kids, item)
	}
	if len(kids) == 0 {
		return nil, fmt.Errorf("empty alternative (an empty branch would match anything — write it as an [ optional ] at the use site)")
	}
	if len(kids) == 1 {
		return kids[0], nil
	}
	return &node{kind: nSeq, kids: kids}, nil
}

func (p *gparser) parseItem() (*node, error) {
	t := p.peek()
	switch t {
	case "(", "{", "[":
		close := map[string]string{"(": ")", "{": "}", "[": "]"}[t]
		kind := map[string]nodeKind{"(": nSeq, "{": nRep, "[": nOpt}[t]
		p.pos++
		inner, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		if p.peek() != close {
			return nil, fmt.Errorf("missing %q", close)
		}
		p.pos++
		if kind == nSeq {
			return inner, nil
		}
		return &node{kind: kind, kids: []*node{inner}}, nil
	}
	p.pos++
	if strings.HasPrefix(t, "'") {
		return &node{kind: nLit, text: strings.Trim(t, "'")}, nil
	}
	if tokenClasses[t] {
		return &node{kind: nClass, text: t}, nil
	}
	if !isRuleName(t) {
		return nil, fmt.Errorf("unexpected %q", t)
	}
	return &node{kind: nRef, text: t, ruleI: -1}, nil
}

// lexGrammar splits a rule body into tokens: quoted terminals, names,
// and the metasyntax punctuation.
func lexGrammar(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'':
			j := i + 1
			for j < len(s) && s[j] != '\'' {
				j++
			}
			if j < len(s) {
				j++
			}
			out = append(out, s[i:j])
			i = j
		case strings.IndexByte("|(){}[]", c) >= 0:
			out = append(out, string(c))
			i++
		default:
			j := i
			for j < len(s) && (isIdentByte(s[j])) {
				j++
			}
			if j == i {
				j++ // never stall on an unexpected byte
			}
			out = append(out, s[i:j])
			i = j
		}
	}
	return out
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isRuleName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isIdentByte(s[i]) {
			return false
		}
	}
	return true
}

func uniq(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func clip(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
