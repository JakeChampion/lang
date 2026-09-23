package ir

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// `-g` must add line tables to the same program, not compile a different one.
//
// The inliner sized callees by len(fn.Ops), and OpLine (native -g) is inserted
// per STATEMENT — so a callee that fits the tiny-leaf carve-out, the loop cap
// or the whole-unit ceiling without -g could miss it with -g, and a different
// set of calls got inlined. On coreutils/cat.fern that gave `-O -g` a
// different `format_chunk` from `-O`, with a different frame size, so a
// profile of the debug build did not describe the release build (#10019).
//
// This lowers the same source twice — markers off, then on — and compares the
// op streams with the markers removed. Any inlining decision that differs
// shows up as a different op sequence, which is the property the issue asks
// for and the one a frame-size or instruction-count check would only sample.
//
// It is the end-to-end half. TestInlineSizeIgnoresLineMarkers covers tiny-leaf
// candidacy and TestTinyLeafBudgetIgnoresLineMarkers the budget, both over the
// unit ceiling; the flat per-site cap this straddles is reached below it, and
// no other test compares the two builds' op streams at all.
func TestLineMarkersDoNotChangeInlining(t *testing.T) {
	// `mid` is sized to STRADDLE the flat per-site cap: its body is under
	// inlineSizeLimit counted as code, and over it once a marker per
	// statement is added. The straddle is asserted below rather than assumed,
	// because a fixture that sits comfortably on one side of the cap cannot
	// fail and an earlier version of this test did exactly that.
	//
	// The call arguments are a VARIABLE, not literals: siteAllows lets a
	// const-arg call through whatever its size (the partial-evaluation
	// carve-out), so literals here would re-hide the difference.
	var b strings.Builder
	b.WriteString("function leaf(a: i32, b: i32): i32 { return a + b; }\n")
	b.WriteString("function mid(n: i32): i32 {\n    var t = 0;\n")
	for i := 0; i < 7; i++ {
		fmt.Fprintf(&b, "    t = t + leaf(n, %d) * %d;\n", i, i+2)
	}
	b.WriteString("    return t;\n}\n")
	b.WriteString("function main(): i32 {\n    var n = 3;\n    return mid(n) + mid(n + 1);\n}\n")
	src := b.String()

	plain, debug := lowerBothWays(t, src)
	find := func(prog *Program, name string) *Func {
		for _, fn := range prog.Funcs {
			if fn.Name == name {
				return fn
			}
		}
		t.Fatalf("no %q in the lowered program", name)
		return nil
	}
	pm, dm := find(plain, "mid"), find(debug, "mid")
	if len(pm.Ops) > inlineSizeLimit || len(dm.Ops) <= inlineSizeLimit {
		t.Fatalf("`mid` no longer straddles the flat cap (%d): %d ops without -g, %d with. "+
			"Retune the statement count — as written this test cannot fail.",
			inlineSizeLimit, len(pm.Ops), len(dm.Ops))
	}

	Inline(plain)
	Inline(debug)
	assertSameCode(t, plain, debug)
}

// lowerBothWays lowers the same source twice, markers off then on. Each leg
// parses and checks its own copy: Inline mutates the program in place, so one
// shared tree would carry the first leg's splices.
func lowerBothWays(t *testing.T, src string) (plain, debug *Program) {
	t.Helper()
	lower := func(withMarkers bool) *Program {
		t.Helper()
		p, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		in, err := checker.Check(p)
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		var opts []LowerOption
		if withMarkers {
			opts = append(opts, EmitLineMarkers())
		}
		out, err := LowerWith(p, in, 8, opts...)
		if err != nil {
			t.Fatalf("lower (markers=%v): %v", withMarkers, err)
		}
		return out
	}
	return lower(false), lower(true)
}

// assertSameCode fails unless the two programs emit the same code once the
// markers are removed — the property `-g` owes the release build.
func assertSameCode(t *testing.T, plain, debug *Program) {
	t.Helper()
	if len(plain.Funcs) != len(debug.Funcs) {
		t.Fatalf("-g changed the function count: %d vs %d", len(plain.Funcs), len(debug.Funcs))
	}
	for i, want := range plain.Funcs {
		got := debug.Funcs[i]
		if want.Name != got.Name {
			t.Fatalf("function %d: %q vs %q", i, want.Name, got.Name)
		}
		gotOps := stripMarkers(got.Ops)
		if len(want.Ops) != len(gotOps) {
			t.Errorf("%s: %d ops without -g, %d with (markers removed) — -g changed which calls were inlined",
				want.Name, len(want.Ops), len(gotOps))
			continue
		}
		for j := range want.Ops {
			if a, c := opKey(want.Ops[j]), opKey(gotOps[j]); a != c {
				t.Errorf("%s op %d: %s without -g, %s with", want.Name, j, a, c)
				break
			}
		}
	}
}

func stripMarkers(ops []Op) []Op {
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		if op.Kind == OpLine || op.Kind == OpCoverPoint {
			continue
		}
		out = append(out, op)
	}
	return out
}

// opKey is the part of an op that decides what code is emitted. Pos is
// deliberately absent: -g is what puts source positions on ops, so comparing
// them would report the very difference the flag is allowed to make.
func opKey(op Op) string {
	var b strings.Builder
	b.WriteString(op.Kind.String())
	b.WriteByte('/')
	b.WriteString(op.Str)
	return b.String()
}
