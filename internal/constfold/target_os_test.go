package constfold

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// targetOSCalls counts the `target_os(...)` calls left in prog.
func targetOSCalls(prog *ast.Program) int {
	n := 0
	ast.WalkProgram(prog, func(node ast.Node) bool {
		if c, ok := node.(*ast.Call); ok {
			if id, ok := c.Callee.(*ast.Ident); ok && id.Name == "target_os" {
				n++
			}
		}
		return true
	})
	return n
}

// `target_os()` becomes the environment the caller compiles for, wherever
// the call sits — a return, a condition, a binding inside a loop body.
func TestFoldWithResolvesTargetOS(t *testing.T) {
	prog, err := parser.Parse(`function os(): string { return target_os(); }
function main(): i32 {
    var n: i32 = 0;
    while (n < 1) {
        var here: string = target_os();
        if (target_os() == "darwin" && here == "darwin") { n = n + 1; }
        n = n + 1;
    }
    return n;
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := FoldWith(prog, Inputs{TargetOS: "darwin"}); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if left := targetOSCalls(prog); left != 0 {
		t.Fatalf("%d target_os() calls survived the fold", left)
	}
	if got := firstStringLit(t, prog); got != "darwin" {
		t.Fatalf("os() returns %q, want the target's environment \"darwin\"", got)
	}
}

// With no target in hand — a bare `-check`, a report — the call is left for
// the checker to type; nothing invents a host.
func TestFoldWithoutTargetLeavesTargetOS(t *testing.T) {
	prog, err := parser.Parse(`function os(): string { return target_os(); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if left := targetOSCalls(prog); left != 1 {
		t.Fatalf("expected the call to survive an untargeted fold, found %d", left)
	}
}

// A call with arguments is not the builtin's shape, so it stays for the
// checker's arity error rather than folding to a literal that hides it.
func TestFoldWithLeavesTargetOSWithArguments(t *testing.T) {
	prog, err := parser.Parse(`function os(): string { return target_os(1); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := FoldWith(prog, Inputs{TargetOS: "linux"}); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if left := targetOSCalls(prog); left != 1 {
		t.Fatalf("expected target_os(1) to survive, found %d calls", left)
	}
}
