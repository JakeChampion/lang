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
	if err := constfold.Fold(prog); err != nil {
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
		if op.Kind == ir.OpCallDirect && op.Str == "__fern_rc_inc" {
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
