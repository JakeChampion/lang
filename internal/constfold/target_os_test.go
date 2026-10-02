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

// A branch on `target_os()` keeps only the arm the target takes once the
// call is a literal: the dead arm's call is gone from the tree, so the
// capability gates and the shake never see it. The live arm stays under
// `if (true)`, which keeps its block's scope.
func TestFoldWithPrunesTargetBranches(t *testing.T) {
	const src = `function main(): i32 {
    if (target_os() == "wasi-http") {
        hosted();
    } else {
        dialled();
    }
    if (target_os() != "linux" && target_arch() == "wasm32") {
        wasm_only();
    }
    return 0;
}`
	for _, tc := range []struct{ os, arch, kept, dropped string }{
		{"wasi-http", "wasm32", "hosted", "dialled"},
		{"linux", "x86-64", "dialled", "hosted"},
	} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := FoldWith(prog, Inputs{TargetOS: tc.os, TargetArch: tc.arch}); err != nil {
			t.Fatalf("fold: %v", err)
		}
		calls := map[string]bool{}
		ifs := 0
		ast.WalkProgram(prog, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.Call:
				if id, ok := n.Callee.(*ast.Ident); ok {
					calls[id.Name] = true
				}
			case *ast.If:
				ifs++
				if b, ok := n.Cond.(*ast.BoolLit); !ok || !b.Value || n.Else != nil {
					t.Errorf("%s: an if on the target survived as %T with else %v", tc.os, n.Cond, n.Else != nil)
				}
			}
			return true
		})
		if !calls[tc.kept] || calls[tc.dropped] {
			t.Errorf("%s: calls after the fold are %v, want %s and not %s", tc.os, calls, tc.kept, tc.dropped)
		}
		if wasm := tc.arch == "wasm32"; calls["wasm_only"] != wasm {
			t.Errorf("%s/%s: wasm_only call present = %v", tc.os, tc.arch, calls["wasm_only"])
		}
		if ifs != 2 {
			t.Errorf("%s: %d ifs after the fold, want 2", tc.os, ifs)
		}
	}
}

// An if-expression is a value, read with both arms: a branch on the
// target folds the call in its condition and keeps every arm, and only the
// statements inside an arm are pruned. The self-host prune reads the same
// rule off the closure the expression desugars to.
func TestFoldWithKeepsIfExpressionArms(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 {
    var n: i32 = if (target_os() == "linux") { 1 } else { 2 };
    return n;
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := FoldWith(prog, Inputs{TargetOS: "linux"}); err != nil {
		t.Fatalf("fold: %v", err)
	}
	found := 0
	ast.WalkProgram(prog, func(node ast.Node) bool {
		x, ok := node.(*ast.IfExpr)
		if !ok {
			return true
		}
		found++
		if x.Else == nil {
			t.Error("if-expression lost its else arm")
		}
		return true
	})
	if found != 1 {
		t.Fatalf("found %d if-expressions, want 1", found)
	}
	if left := targetOSCalls(prog); left != 0 {
		t.Fatalf("%d target_os() calls survived the fold", left)
	}
}
