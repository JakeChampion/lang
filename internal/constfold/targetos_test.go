package constfold

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// foldFor is fold() with a named target, so a test can say what
// __fern_target_os() should answer with (#8338).
func foldFor(t *testing.T, targetOS, src string) *ast.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Fold(prog, nil, ForTarget(targetOS)); err != nil {
		t.Fatalf("fold: %v", err)
	}
	return prog
}

// The builtin resolves to the target's environment, in a const initialiser
// and in ordinary expression position alike.
func TestTargetOSResolvesInConst(t *testing.T) {
	prog := foldFor(t, "darwin", `const OS: string = __fern_target_os();
function main(): string { return OS; }`)
	lit, ok := returnLit(t, prog).(*ast.StringLit)
	if !ok {
		t.Fatalf("return value should be StringLit, got %T", returnLit(t, prog))
	}
	if lit.Value != "darwin" {
		t.Errorf("got %q, want darwin", lit.Value)
	}
}

func TestTargetOSResolvesInFunctionBody(t *testing.T) {
	prog := foldFor(t, "wasi", `function main(): string { return __fern_target_os(); }`)
	lit, ok := returnLit(t, prog).(*ast.StringLit)
	if !ok {
		t.Fatalf("return value should be StringLit, got %T", returnLit(t, prog))
	}
	if lit.Value != "wasi" {
		t.Errorf("got %q, want wasi", lit.Value)
	}
}

// The whole point: a comparison against the target folds to a bool literal,
// so the branch it guards is dead code before the checker ever sees it. The
// const spelling and the inline one must both get there — native folded only
// the const one until #8338, while the self-host's folder did both.
func TestTargetOSComparisonFoldsToBool(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"inline-false", `function main(): boolean { return __fern_target_os() == "darwin"; }`, false},
		{"inline-true", `function main(): boolean { return __fern_target_os() == "linux"; }`, true},
		{"inline-ne", `function main(): boolean { return __fern_target_os() != "darwin"; }`, true},
		{"via-const", `const IS_DARWIN: boolean = __fern_target_os() == "darwin";
function main(): boolean { return IS_DARWIN; }`, false},
		{"grouped", `const OS: string = __fern_target_os();
const IS_LINUXY: boolean = OS == "linux" || OS == "android";
function main(): boolean { return IS_LINUXY; }`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog := foldFor(t, "linux", tc.src)
			lit, ok := returnLit(t, prog).(*ast.BoolLit)
			if !ok {
				t.Fatalf("%s: return value should be BoolLit (the comparison must fold, or the branch survives to codegen), got %T",
					tc.name, returnLit(t, prog))
			}
			if lit.Value != tc.want {
				t.Errorf("%s: got %v, want %v", tc.name, lit.Value, tc.want)
			}
		})
	}
}

// Concatenation folds too, so `"prefix-" + __fern_target_os()` is one literal.
func TestTargetOSConcatFolds(t *testing.T) {
	prog := foldFor(t, "android", `function main(): string { return "on-" + __fern_target_os(); }`)
	lit, ok := returnLit(t, prog).(*ast.StringLit)
	if !ok {
		t.Fatalf("return value should be StringLit, got %T", returnLit(t, prog))
	}
	if lit.Value != "on-android" {
		t.Errorf("got %q, want on-android", lit.Value)
	}
}

// A caller that names no target gets the default target's OS, not the host's:
// the answer must not depend on which machine the compiler runs on.
func TestTargetOSDefaultsToDefaultTarget(t *testing.T) {
	prog := fold(t, `function main(): string { return __fern_target_os(); }`)
	lit, ok := returnLit(t, prog).(*ast.StringLit)
	if !ok {
		t.Fatalf("return value should be StringLit, got %T", returnLit(t, prog))
	}
	if lit.Value != defaultTargetOS {
		t.Errorf("got %q, want %q", lit.Value, defaultTargetOS)
	}
}

// Only a literal-to-literal comparison folds; a runtime operand leaves the
// binary alone rather than folding against a stale half.
func TestTargetOSDoesNotFoldRuntimeOperand(t *testing.T) {
	prog := foldFor(t, "linux", `function main(s: string): boolean { return __fern_target_os() == s; }`)
	var got ast.Expr
	for _, fn := range prog.Funcs {
		if fn.Name == "main" {
			got = fn.Body.Stmts[0].(*ast.Return).Value
		}
	}
	bin, ok := got.(*ast.Binary)
	if !ok {
		t.Fatalf("comparison against a parameter must stay a Binary, got %T", got)
	}
	if _, ok := bin.Left.(*ast.StringLit); !ok {
		t.Errorf("the builtin half should still have been substituted, got %T", bin.Left)
	}
}

// Arity is checked here rather than left to reach the checker as an
// undefined function.
func TestTargetOSRejectsArguments(t *testing.T) {
	prog, err := parser.Parse(`function main(): string { return __fern_target_os("linux"); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = Fold(prog, nil, ForTarget("linux"))
	if err == nil {
		t.Fatal("expected an arity error")
	}
	if !strings.Contains(err.Error(), "takes no arguments") {
		t.Errorf("error should name the arity, got: %v", err)
	}
}
