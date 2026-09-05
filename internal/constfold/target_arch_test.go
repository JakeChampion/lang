package constfold

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// targetArchCalls counts the `target_arch(...)` calls left in prog.
func targetArchCalls(prog *ast.Program) int {
	n := 0
	ast.WalkProgram(prog, func(node ast.Node) bool {
		if c, ok := node.(*ast.Call); ok {
			if id, ok := c.Callee.(*ast.Ident); ok && id.Name == "target_arch" {
				n++
			}
		}
		return true
	})
	return n
}

// `target_arch()` becomes the ISA the caller compiles for, wherever the call
// sits, and folds independently of the environment half: naming one must not
// resolve or disturb the other.
func TestFoldWithResolvesTargetArch(t *testing.T) {
	prog, err := parser.Parse(`function arch(): string { return target_arch(); }
function main(): i32 {
    var n: i32 = 0;
    while (n < 1) {
        var here: string = target_arch();
        if (target_arch() == "x86-64" && here == "x86-64") { n = n + 1; }
        n = n + 1;
    }
    return n;
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := FoldWith(prog, Inputs{TargetArch: "x86-64"}); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if left := targetArchCalls(prog); left != 0 {
		t.Fatalf("%d target_arch() calls survived the fold", left)
	}
	if got := firstStringLit(t, prog); got != "x86-64" {
		t.Fatalf("arch() returns %q, want the target's ISA \"x86-64\"", got)
	}
}

// Each half is folded by its own input: an environment with no ISA resolves
// target_os() and leaves target_arch() for the checker, and the reverse.
func TestFoldWithResolvesEachHalfSeparately(t *testing.T) {
	src := `function both(): string { return target_os() + target_arch(); }`

	osOnly, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := FoldWith(osOnly, Inputs{TargetOS: "linux"}); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if left := targetOSCalls(osOnly); left != 0 {
		t.Fatalf("%d target_os() calls survived a fold that named the environment", left)
	}
	if left := targetArchCalls(osOnly); left != 1 {
		t.Fatalf("target_arch() folded with no ISA named, %d calls left", left)
	}

	archOnly, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := FoldWith(archOnly, Inputs{TargetArch: "arm64"}); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if left := targetArchCalls(archOnly); left != 0 {
		t.Fatalf("%d target_arch() calls survived a fold that named the ISA", left)
	}
	if left := targetOSCalls(archOnly); left != 1 {
		t.Fatalf("target_os() folded with no environment named, %d calls left", left)
	}
}

// A call with arguments is not the builtin's shape, so it stays for the
// checker's arity error rather than folding to a literal that hides it.
func TestFoldWithLeavesTargetArchWithArguments(t *testing.T) {
	prog, err := parser.Parse(`function arch(): string { return target_arch(1); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := FoldWith(prog, Inputs{TargetArch: "arm64"}); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if left := targetArchCalls(prog); left != 1 {
		t.Fatalf("expected target_arch(1) to survive, found %d calls", left)
	}
}
