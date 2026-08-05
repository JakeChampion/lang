package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

func lowerForTest(t *testing.T, src string) *ir.Program {
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
	ast.RcFreeEnabled = true
	ip, err := ir.LowerWith(prog, info, 8)
	ast.RcFreeEnabled = prev
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return ip
}

func incCount(fn *ir.Func) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpRcInc {
			n++
		}
	}
	return n
}

func funcByName(ip *ir.Program, name string) *ir.Func {
	for _, fn := range ip.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	return nil
}

// Move-on-return: `return <owned rc local>` emits neither the
// return-transfer inc nor that local's exit-sweep dec (they cancel).
func TestMoveOnReturnElidesIncForOwnedLocal(t *testing.T) {
	ip := lowerForTest(t, `function f(): i32[] {
    var x: i32[] = [1, 2, 3];
    return x;
}
function main(): i32 { return f()[0]; }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got != 0 {
		t.Errorf("return of owned local should emit no __fern_rc_inc, got %d", got)
	}
}

// Move-on-alias: `var y = x; return y` where x is read only once (the
// alias) elides the alias inc too — combined with move-on-return the
// whole pass-through carries zero rc traffic (no __fern_rc_inc).
func TestMoveOnAliasElidesIncForSingleUseLocal(t *testing.T) {
	ip := lowerForTest(t, `function f(): i32[] {
    var x: i32[] = [1, 2, 3];
    var y: i32[] = x;
    return y;
}
function main(): i32 { return f()[0]; }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got != 0 {
		t.Errorf("single-use alias + return should emit no __fern_rc_inc, got %d", got)
	}
}

// General last-use: a local used MORE than once still moves when its
// LAST occurrence is a top-level alias (here `x[0]` reads x, then the
// alias is x's final use). No __fern_rc_inc should remain.
func TestMoveOnAliasMovesAtLastUseEvenIfMultiUse(t *testing.T) {
	ip := lowerForTest(t, `function f(): i32[] {
    var x: i32[] = [1, 2, 3];
    var n: i32 = x[0];
    var y: i32[] = x;
    return y;
}
function main(): i32 { return f()[0]; }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got != 0 {
		t.Errorf("multi-use local whose last use is a top-level alias should move (0 incs), got %d", got)
	}
}

// Dominance guard: an alias inside a branch does NOT move even when it
// is the local's last occurrence — on the not-taken path x is still
// live and must be swept, so the inc is kept.
func TestMoveOnAliasKeepsIncForBranchedAlias(t *testing.T) {
	ip := lowerForTest(t, `function f(c: boolean): i32 {
    var x: i32[] = [1, 2, 3];
    if (c) {
        var y: i32[] = x;
        return y[0];
    }
    return 0;
}
function main(): i32 { return f(true); }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got == 0 {
		t.Errorf("branched alias must keep its inc (not dominate the exit), got 0")
	}
}

// A local READ more than once is NOT moved — its alias keeps the inc,
// since the later read needs the value live.
func TestMoveOnAliasKeepsIncWhenReadAgain(t *testing.T) {
	ip := lowerForTest(t, `function f(): i32 {
    var x: i32[] = [1, 2, 3];
    var y: i32[] = x;
    return x[0] + y[0];
}
function main(): i32 { return f(); }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got == 0 {
		t.Errorf("alias of a multi-use local must keep its inc, got 0")
	}
}

// Returning a borrowed PARAM still needs the transfer inc — the exit
// sweep does NOT dec params, so there's no dec to cancel against.
func TestMoveOnReturnKeepsIncForParam(t *testing.T) {
	ip := lowerForTest(t, `function g(p: i32[]): i32[] {
    return p;
}
function main(): i32 { return g([1, 2, 3])[0]; }`)
	g := funcByName(ip, "g")
	if g == nil {
		t.Fatal("no func g")
	}
	if got := incCount(g); got != 1 {
		t.Errorf("return of borrowed param should keep one __fern_rc_inc, got %d", got)
	}
}
