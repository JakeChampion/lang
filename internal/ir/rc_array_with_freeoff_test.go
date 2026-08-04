package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

// Free-off is the no-reclamation baseline: with ast.RcFreeEnabled=false the
// lowering must emit NO __drop_* calls at all, because the flag-gated helper
// worklist that generates their bodies (__drop_struct_/__drop_enum_/…) never
// runs — an ungated call site therefore links against an undefined symbol.
// Regression shape from #4956's std/regex Pike-VM (regex_captures fixtures):
// `.with(i, v)` on an array of enums with a pointer payload routed through
// emitArraySet's rcTracked branch, whose old-element dropStructField was not
// gated on RcFreeEnabled, leaving free-off builds (the FreeMatchesNoFree
// differentials on every backend) with an undefined __drop_enum_<E> reference.
const arrayWithEnumSrc = `
enum E { S(string), N(i32) }

function patch(xs: E[], i: i32): E[] {
    var ys: E[] = xs;
    ys = ys.with(i, N(7));
    return ys;
}

function main(): i32 {
    var a: E[] = [S("hello"), N(1)];
    a = patch(a, 0);
    return a.len();
}`

func lowerWithFreeFlag(t *testing.T, src string, freeOn bool) *ir.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = freeOn
	defer func() { ast.RcFreeEnabled = prev }()
	ip, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return ip
}

func dropCallsAndDefs(ip *ir.Program) (calls []string, defined map[string]bool) {
	defined = map[string]bool{}
	for _, fn := range ip.Funcs {
		if strings.HasPrefix(fn.Name, "__drop_") {
			defined[fn.Name] = true
		}
		for _, op := range fn.Ops {
			if op.Kind == ir.OpCallDirect && strings.HasPrefix(op.Str, "__drop_") {
				calls = append(calls, fn.Name+" -> "+op.Str)
			}
		}
	}
	return calls, defined
}

func TestArrayWithEnumElemFreeOffEmitsNoDropCalls(t *testing.T) {
	ip := lowerWithFreeFlag(t, arrayWithEnumSrc, false)
	if calls, _ := dropCallsAndDefs(ip); len(calls) != 0 {
		t.Fatalf("free-off lowering emitted __drop_* calls (bodies are only generated under RcFreeEnabled, so these are undefined references at link):\n  %s",
			strings.Join(calls, "\n  "))
	}
}

// Companion direction check: the same program under free-ON must still route
// the .with old-element release through the generated __drop_enum_E, and the
// worklist must define it. This keeps the free-off assertion above meaningful
// (proves the shape exercises emitArraySet's rcTracked drop path at all).
func TestArrayWithEnumElemFreeOnDropIsGenerated(t *testing.T) {
	ip := lowerWithFreeFlag(t, arrayWithEnumSrc, true)
	calls, defined := dropCallsAndDefs(ip)
	sawEnumDrop := false
	for _, c := range calls {
		callee := c[strings.Index(c, " -> ")+4:]
		if callee == "__drop_enum_E" {
			sawEnumDrop = true
		}
		if !defined[callee] {
			t.Errorf("free-on lowering calls %s with no generated body", c)
		}
	}
	if !sawEnumDrop {
		t.Fatalf("free-on lowering never called __drop_enum_E — the fixture shape no longer exercises emitArraySet's rcTracked old-element drop; calls seen:\n  %s",
			strings.Join(calls, "\n  "))
	}
}
