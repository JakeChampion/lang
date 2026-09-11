package coreutils

import (
	"bytes"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The randomized differential over `expr SUBJ : PATTERN`.
//
// The hand-written corpus in expr_test.go is a list of cases someone
// thought of, and a disambiguation rule is exactly the thing nobody
// thinks of: `\(\|a\)a*` over `aa` reports `a` for the group under
// glibc and reported the empty first branch here (#9051), and no case
// in the corpus came near it. A sweep of random patterns over random
// subjects, both sides asked the same question, finds that class
// without anyone naming it first.
//
// Every run is SEEDED, so a failure is reproducible: FERN_EXPR_FUZZ_SEED
// picks another one and FERN_EXPR_FUZZ_CASES another count. A failure is
// not reported as the seed, though — a seed is a way back to a 30-byte
// pattern nobody can read. It is shrunk against the same oracle, one
// simplification at a time, until nothing smaller still diverges, and
// what the failure names is that.
//
// The seed below is green. Others are not: sweeping 1-20 at 4000 cases
// left seven seeds red, every one of them on a shape #9092 records with
// a minimal input — a SIGSEGV in GNU expr, a match GNU never returns
// from, four patterns glibc declares unmatched though they match, and
// an empty alternation branch inside a counted repetition, where
// glibc's answer is not a branch order at all (compile_rep in
// coreutils/lib/bre.fern). All seven are older than this file. A
// failure naming one of those inputs is that, not a regression.
const (
	exprFuzzSeed  = 0x9051
	exprFuzzCases = 3000
	// How many distinct divergences to shrink and report before
	// stopping. More than one because a sweep that only ever reports
	// its first failure hides every other one behind it.
	exprFuzzReports = 5
	// What either side gets to answer one invocation in. Generously
	// above the milliseconds an answer takes, because the bound is not
	// a performance assertion: it is there because GNU expr sometimes
	// never answers at all (#9092), and a run that outlasts it is
	// reported as the divergence it is.
	exprFuzzRunLimit = 5 * time.Second
)

func TestExprRegexDifferential(t *testing.T) {
	seed := uint64(exprFuzzSeed)
	if s := os.Getenv("FERN_EXPR_FUZZ_SEED"); s != "" {
		n, err := strconv.ParseUint(s, 0, 64)
		if err != nil {
			t.Fatalf("FERN_EXPR_FUZZ_SEED=%q: %v", s, err)
		}
		seed = n
	}
	cases := exprFuzzCases
	if s := os.Getenv("FERN_EXPR_FUZZ_CASES"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			t.Fatalf("FERN_EXPR_FUZZ_CASES=%q: want a positive count", s)
		}
		cases = n
	}
	ours := fernBin(t, "expr")
	ref := referenceBin(t, "expr")
	_, ver := gnuDir(t)
	t.Logf("reference: %s (%s); seed %#x, %d cases", ref, ver, seed, cases)

	agree := func(subj, pat string) bool {
		inv := invocation{args: []string{subj, ":", pat}, timeout: exprFuzzRunLimit}
		want := inv.run(t, ref, "expr")
		got := inv.run(t, ours, "expr")
		return bytes.Equal(want.stdout, got.stdout) && bytes.Equal(want.stderr, got.stderr) && want.how() == got.how()
	}

	r := rand.New(rand.NewPCG(seed, seed^0x5bf03635))
	reported := 0
	for i := 0; i < cases; i++ {
		p := genTop(r)
		subj := genSubject(r)
		if agree(subj, p.render()) {
			continue
		}
		subj, p = shrinkExprCase(subj, p, agree)
		inv := invocation{args: []string{subj, ":", p.render()}, timeout: exprFuzzRunLimit}
		want := inv.run(t, ref, "expr")
		got := inv.run(t, ours, "expr")
		t.Errorf("expr %s differs (case %d of seed %#x, shrunk)\n gnu: %s / %s / %s\nfern: %s / %s / %s",
			quoteArgs(inv.args), i, seed,
			quote(want.stdout), quote(want.stderr), want.how(),
			quote(got.stdout), quote(got.stderr), got.how())
		reported++
		if reported == exprFuzzReports {
			t.Fatalf("stopping after %d divergences", reported)
		}
	}
}

// ---- the generated pattern -------------------------------------------------
//
// A tree rather than a string, because the shrinker has to hand back
// patterns that still PARSE: dropping a byte from `\(ab\|c\)*` reaches
// `\(ab\|c\*` far more often than it reaches anything meaningful, and a
// corpus of syntax errors is not what diverged.

type patKind int

const (
	pkLit patKind = iota
	pkAny
	pkClass
	pkBOL
	pkEOL
	pkCat
	pkAlt
	pkGroup
	pkRep
	pkBackref
	pkNothing // an empty alternation branch
)

type patNode struct {
	kind patKind
	// lit is the byte for pkLit and the bracket body for pkClass.
	lit  string
	kids []*patNode
	// rep is the operator text for pkRep — `*`, `\?`, `\+`, `\{m,n\}`.
	rep string
	// ref is the 1-based group for pkBackref.
	ref int
}

func (p *patNode) render() string {
	var b strings.Builder
	p.write(&b, new(int))
	return b.String()
}

// write appends this node's text, numbering groups in the order their
// `\(` is written — which is how a backreference finds the group it
// names, so the numbering happens here rather than when the tree was
// built, where a shrink can still move a group.
func (p *patNode) write(b *strings.Builder, groups *int) {
	switch p.kind {
	case pkLit:
		b.WriteString(p.lit)
	case pkAny:
		b.WriteString(".")
	case pkClass:
		b.WriteString("[" + p.lit + "]")
	case pkBOL:
		b.WriteString("^")
	case pkEOL:
		b.WriteString("$")
	case pkNothing:
	case pkCat:
		for _, k := range p.kids {
			k.write(b, groups)
		}
	case pkAlt:
		for i, k := range p.kids {
			if i > 0 {
				b.WriteString(`\|`)
			}
			k.write(b, groups)
		}
	case pkGroup:
		*groups++
		b.WriteString(`\(`)
		p.kids[0].write(b, groups)
		b.WriteString(`\)`)
	case pkRep:
		p.kids[0].write(b, groups)
		b.WriteString(p.rep)
	case pkBackref:
		// A reference past the groups actually written is a compile
		// error on both sides rather than a match, which proves
		// nothing about disambiguation; clamp it to a group that
		// exists, and drop it when none does.
		ref := p.ref
		if ref > *groups {
			ref = *groups
		}
		if ref >= 1 {
			b.WriteString(`\` + strconv.Itoa(ref))
		}
	}
}

func (p *patNode) clone() *patNode {
	c := *p
	c.kids = make([]*patNode, len(p.kids))
	for i, k := range p.kids {
		c.kids[i] = k.clone()
	}
	return &c
}

// genSubject draws a short string over a two-letter alphabet. Short and
// narrow on purpose: a divergence needs the pattern to have more than
// one way to reach the same match, and over `abcdef…` a random pattern
// almost never matches at all.
func genSubject(r *rand.Rand) string {
	n := r.IntN(6)
	b := make([]byte, n)
	for i := range b {
		b[i] = "aab"[r.IntN(3)]
	}
	return string(b)
}

// genTop shapes the whole pattern around what the oracle can be read
// through: `expr :` reports GROUP 1 and nothing else, so a draw whose
// first group is not where the ambiguity sits proves only the overall
// match length. Two thirds of them are therefore `\(X\)Y`, which is
// also the shape expr is used in; the rest are unconstrained, so the
// sweep still covers patterns with no group at all.
func genTop(r *rand.Rand) *patNode {
	if r.IntN(3) == 0 {
		return genPat(r, 4)
	}
	g := &patNode{kind: pkGroup, kids: []*patNode{genPat(r, 3)}}
	if r.IntN(4) == 0 {
		return g
	}
	return &patNode{kind: pkCat, kids: []*patNode{g, genPat(r, 3)}}
}

func genPat(r *rand.Rand, depth int) *patNode {
	if depth <= 0 {
		return genAtom(r)
	}
	switch r.IntN(10) {
	case 0, 1, 2:
		kids := make([]*patNode, 2+r.IntN(2))
		for i := range kids {
			kids[i] = genPat(r, depth-1)
		}
		return &patNode{kind: pkCat, kids: kids}
	case 3, 4, 5:
		kids := make([]*patNode, 2+r.IntN(2))
		for i := range kids {
			// An empty branch is drawn deliberately often: it is the
			// shape #9051 turned on, and a generator that reaches it
			// only by accident reaches it never.
			if r.IntN(4) == 0 {
				kids[i] = &patNode{kind: pkNothing}
			} else {
				kids[i] = genPat(r, depth-1)
			}
		}
		return &patNode{kind: pkAlt, kids: kids}
	case 6, 7:
		return &patNode{kind: pkGroup, kids: []*patNode{genPat(r, depth-1)}}
	case 8:
		return &patNode{kind: pkRep, rep: genRepOp(r), kids: []*patNode{genPat(r, depth-1)}}
	default:
		return genAtom(r)
	}
}

func genRepOp(r *rand.Rand) string {
	switch r.IntN(5) {
	case 0:
		return `\?`
	case 1:
		return `\+`
	case 2:
		lo := r.IntN(3)
		return `\{` + strconv.Itoa(lo) + `,` + strconv.Itoa(lo+r.IntN(3)) + `\}`
	case 3:
		return `\{` + strconv.Itoa(r.IntN(3)) + `\}`
	default:
		return "*"
	}
}

func genAtom(r *rand.Rand) *patNode {
	switch r.IntN(12) {
	case 0:
		return &patNode{kind: pkAny}
	case 1:
		return &patNode{kind: pkClass, lit: []string{"ab", "^a", "a-b", "abc"}[r.IntN(4)]}
	case 2:
		return &patNode{kind: pkBOL}
	case 3:
		return &patNode{kind: pkEOL}
	case 4:
		return &patNode{kind: pkBackref, ref: 1 + r.IntN(2)}
	default:
		return &patNode{kind: pkLit, lit: string("aab"[r.IntN(3)])}
	}
}

// ---- shrinking -------------------------------------------------------------

// shrinkExprCase reduces a diverging (subject, pattern) to one nothing
// smaller still diverges at, asking the same oracle at every step. Each
// pass walks the subject first and the tree second, adopts the first
// candidate that still diverges, and starts over; it stops when a whole
// pass finds none.
func shrinkExprCase(subj string, p *patNode, agree func(subj, pat string) bool) (string, *patNode) {
	diverges := func(s string, n *patNode) bool { return !agree(s, n.render()) }
	for round := 0; round < 200; round++ {
		shrunk := false
		for _, cand := range shrinkSubject(subj) {
			if diverges(cand, p) {
				subj, shrunk = cand, true
				break
			}
		}
		if shrunk {
			continue
		}
		for _, cand := range shrinkPat(p) {
			if diverges(subj, cand) {
				p, shrunk = cand, true
				break
			}
		}
		if !shrunk {
			break
		}
	}
	return subj, p
}

func shrinkSubject(s string) []string {
	var out []string
	for i := range s {
		out = append(out, s[:i]+s[i+1:])
	}
	for i := range s {
		if s[i] != 'a' {
			out = append(out, s[:i]+"a"+s[i+1:])
		}
	}
	return out
}

// shrinkPat lists the patterns one simplification away from p, simplest
// first: this node replaced by something smaller, then each child
// replaced by one of its own candidates.
func shrinkPat(p *patNode) []*patNode {
	out := shrinkNode(p)
	for i, k := range p.kids {
		for _, sk := range shrinkPat(k) {
			c := p.clone()
			c.kids[i] = sk
			out = append(out, c)
		}
	}
	return out
}

// shrinkNode lists the replacements for one node alone, leaving its
// children's own shrinks to shrinkPat.
func shrinkNode(p *patNode) []*patNode {
	var out []*patNode
	switch p.kind {
	case pkCat, pkAlt:
		for _, k := range p.kids {
			out = append(out, k.clone())
		}
		if len(p.kids) > 2 {
			for i := range p.kids {
				c := p.clone()
				c.kids = append(c.kids[:i:i], c.kids[i+1:]...)
				out = append(out, c)
			}
		}
	case pkGroup, pkRep:
		out = append(out, p.kids[0].clone())
		if p.kind == pkRep && p.rep != "*" {
			c := p.clone()
			c.rep = "*"
			out = append(out, c)
		}
	case pkClass, pkAny, pkBackref:
		out = append(out, &patNode{kind: pkLit, lit: "a"})
	case pkLit:
		if p.lit != "a" {
			out = append(out, &patNode{kind: pkLit, lit: "a"})
		}
	}
	return out
}
