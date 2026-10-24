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

const (
	exprFuzzSeed     = 0x9051
	exprFuzzCases    = 3000
	exprFuzzReports  = 5
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
	pkNothing
)

type patNode struct {
	kind patKind
	lit  string
	kids []*patNode
	rep  string
	ref  int
}

func (p *patNode) render() string {
	var b strings.Builder
	p.write(&b, new(int))
	return b.String()
}

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

func genSubject(r *rand.Rand) string {
	n := r.IntN(6)
	b := make([]byte, n)
	for i := range b {
		b[i] = "aab"[r.IntN(3)]
	}
	return string(b)
}

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
