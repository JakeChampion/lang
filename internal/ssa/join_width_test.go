package ssa

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// How wide is a join?
//
// #7786 records that Roc's certifier carries a lattice of PARTITIONS of
// the refcounted locals live at a join — so its cost is a Bell number
// of the width, B(12) = 4.2 million, which is a scaling failure they
// hit and had to fix. It says to plan the summarisation up front rather
// than discover the same wall. This measures the wall.
//
// Two things it settles, one of them by refutation:
//
//   - Collapsing by ALIAS CLASS buys nothing. The hypothesis was that
//     SSA already represents aliasing explicitly, so the partition
//     would be derivable and the effective width would drop. Measured:
//     16.00% of joins wider than 12 by value, 15.73% by alias class.
//     Distinct live values at a join are almost always distinct
//     objects already.
//
//   - No lattice exponential in the width is viable, and Bell numbers
//     are not the binding constraint — the width is. At p99 = 157 and
//     a maximum of 1879, 2^n is as hopeless as B(n).
//
// It is a measurement with a ceiling rather than a correctness gate:
// what would matter is the width GROWING to where even a per-value
// summary is costly, since that is the assumption the certifier design
// rests on. See docs/rc-log/2026-08-30-join-width.md.
func TestJoinWidthStaysBelowWhatALatticeCouldCarry(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the whole self-host compiler; not a -short test")
	}
	path := filepath.Join("..", "..", "examples", "self_host", "fern.fern")
	prog, _, err := modload.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatal(err)
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	ip, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		t.Fatal(err)
	}
	shapes := ir.NewCallShapes(ip)

	var joins int
	var classHist, valHist []int
	maxClasses, maxVals := 0, 0
	var worst string
	for _, fn := range ip.Funcs {
		f, err := LiftFromIRWith(fn, shapes)
		if err != nil {
			continue
		}
		if len(f.Blocks) > 400 {
			continue
		}
		uses := BuildUses(f)
		live := liveAtBlockStart(f)
		ptr := pointerish(f)
		for bi, b := range f.Blocks {
			if len(b.Preds) < 2 {
				continue
			}
			joins++
			// Values live into the join that reference counting can
			// apply to at all.
			var vals []Value
			for _, v := range live[bi] {
				if ptr[v.ID] {
					vals = append(vals, v)
				}
			}
			// Collapse them by the alias closure the SSA already
			// records.
			rep := map[int32]int32{}
			classes := 0
			for _, v := range vals {
				if _, done := rep[v.ID]; done {
					continue
				}
				classes++
				for _, a := range aliasesOf(f, uses, v) {
					if _, done := rep[a.ID]; !done {
						rep[a.ID] = v.ID
					}
				}
			}
			classHist = append(classHist, classes)
			valHist = append(valHist, len(vals))
			if classes > maxClasses {
				maxClasses, worst = classes, f.Name
			}
			if len(vals) > maxVals {
				maxVals = len(vals)
			}
		}
	}
	sort.Ints(classHist)
	sort.Ints(valHist)
	pct := func(h []int, p float64) int {
		if len(h) == 0 {
			return 0
		}
		i := int(float64(len(h)) * p)
		if i >= len(h) {
			i = len(h) - 1
		}
		return h[i]
	}
	over := func(h []int, n int) float64 {
		c := 0
		for _, x := range h {
			if x > n {
				c++
			}
		}
		return 100 * float64(c) / float64(len(h))
	}
	t.Logf("joins: %d", joins)
	t.Logf("  live pointer VALUES  p50=%d p90=%d p99=%d max=%d  (>12: %.2f%%)",
		pct(valHist, .5), pct(valHist, .9), pct(valHist, .99), maxVals, over(valHist, 12))
	t.Logf("  ALIAS CLASSES        p50=%d p90=%d p99=%d max=%d  (>12: %.2f%%)  worst=%s",
		pct(classHist, .5), pct(classHist, .9), pct(classHist, .99), maxClasses, over(classHist, 12), worst)

	if joins < 10000 {
		t.Fatalf("only %d joins seen; this is no longer measuring the compiler", joins)
	}
	// Measured 2026-08-30 at p99 = 157 (values) / 154 (classes) and a
	// maximum of 1879. The ceiling is loose on purpose: the number is
	// expected to move with the compiler, and what it is here to catch
	// is a change in ORDER, which is what would invalidate the design
	// this constrains.
	const p99Ceiling = 400
	if got := pct(classHist, .99); got > p99Ceiling {
		t.Errorf("p99 join width is %d alias classes, over the %d ceiling — "+
			"a per-value summary was affordable on the assumption this stayed small",
			got, p99Ceiling)
	}
}

// pointerish resolves, per function, which SSA values reference
// counting can apply to. A phi is pointer-ish when any of its arguments
// is, which needs a fixpoint since phis feed phis around a loop.
func pointerish(f *Func) map[int32]bool {
	def := map[int32]*Op{}
	for _, b := range f.Blocks {
		for _, o := range b.Ops {
			if o.Result.IsValid() {
				def[o.Result.ID] = o
			}
		}
	}
	out := map[int32]bool{}
	for i, p := range f.Params {
		if i < len(f.ParamAddrs) && f.ParamAddrs[i] {
			out[p.ID] = true
		}
	}
	for id, o := range def {
		if o.Kind != OpPhi && (o.Addr || o.Kind == OpAlloc) {
			out[id] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for id, o := range def {
			if o.Kind != OpPhi || out[id] {
				continue
			}
			for _, a := range o.Args {
				if out[a.ID] {
					out[id] = true
					changed = true
					break
				}
			}
		}
	}
	return out
}

// liveAtBlockStart is a plain backward dataflow to a fixpoint.
func liveAtBlockStart(f *Func) [][]Value {
	n := len(f.Blocks)
	idx := map[*Block]int{}
	for i, b := range f.Blocks {
		idx[b] = i
	}
	in := make([]map[int32]Value, n)
	for i := range in {
		in[i] = map[int32]Value{}
	}
	for changed := true; changed; {
		changed = false
		for bi := n - 1; bi >= 0; bi-- {
			b := f.Blocks[bi]
			cur := map[int32]Value{}
			for _, sb := range b.Succs() {
				for id, v := range in[idx[sb]] {
					cur[id] = v
				}
			}
			for i := len(b.Ops) - 1; i >= 0; i-- {
				o := b.Ops[i]
				delete(cur, o.Result.ID)
				for _, a := range o.Args {
					if a.IsValid() {
						cur[a.ID] = a
					}
				}
			}
			if len(cur) != len(in[bi]) {
				changed = true
			}
			in[bi] = cur
		}
	}
	out := make([][]Value, n)
	for i, m := range in {
		for _, v := range m {
			out[i] = append(out[i], v)
		}
		sort.Slice(out[i], func(a, b int) bool { return out[i][a].ID < out[i][b].ID })
	}
	return out
}
