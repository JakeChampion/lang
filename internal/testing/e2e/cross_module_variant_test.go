package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

// Cross-module variant-pattern matching: a sibling module declares
// a public union, and the entry module's `match` arms reference its
// variants with a `mod.` qualifier (`tokens.TokA(x) => …`). Until
// this PR the parser bailed at the `.` and modload had no machinery
// to mangle the variant name or the union/enum decl. The fix spans
// parser (accept the qualifier), ast (record VariantModule), modload
// (mangle EnumDecl / UnionDecl names + variant patterns + export
// visibility), and checker (validate the qualifier against the
// scrutinee enum's SourceModule).
//
// The project is two files on disk so import resolution is exercised
// end-to-end rather than through a single-source helper.

const crossModuleVariantTokens = `pub struct TokA { x: i32 }
pub struct TokB { y: i32 }
pub type Tok = TokA | TokB;
pub function make_a(): Tok { return TokA { x: 5 }; }
pub function make_b(): Tok { return TokB { y: 17 }; }
`

const crossModuleVariantMain = `
import "./tokens";

function main(): i32 {
    let t1: tokens.Tok = tokens.make_a();
    let v1: i32 = 0;
    match (t1) {
        tokens.TokA(a) => { v1 = a.x; },
        tokens.TokB(b) => { v1 = b.y; }
    }
    let t2: tokens.Tok = tokens.make_b();
    let v2: i32 = 0;
    match (t2) {
        tokens.TokA(a) => { v2 = a.x; },
        tokens.TokB(b) => { v2 = b.y; }
    }
    // v1 == 5, v2 == 17 → 22.
    return v1 + v2;
}
`

func writeCrossModuleVariantProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tokens.fern"), []byte(crossModuleVariantTokens), 0o644); err != nil {
		t.Fatalf("write tokens.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.fern"), []byte(crossModuleVariantMain), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	return dir
}

func TestCrossModuleVariantPatternX86_64(t *testing.T) {
	dir := writeCrossModuleVariantProject(t)
	if _, got := runFixtureX86_64(t, filepath.Join(dir, "main.fern"), ""); got != 22 {
		t.Errorf("exit code: got %d, want 22", got)
	}
}

func TestCrossModuleVariantPatternArm64(t *testing.T) {
	dir := writeCrossModuleVariantProject(t)
	if _, got := runFixtureArm64(t, filepath.Join(dir, "main.fern"), ""); got != 22 {
		t.Errorf("exit code: got %d, want 22", got)
	}
}

// Mismatched module qualifier: declaring `lexer.TokA(_)` when the
// scrutinee enum lives in `tokens` should be a checker-time error,
// not silently accepted. Confirms the SourceModule comparison in
// checker.go fires.
// Once import aliases landed, a module can be referred to by both an
// alias and its basename, and a variant pattern qualified with either
// name resolves to the same module. (Before aliases this shape was
// rejected — `import ... as lexer;` was a parse error — and this test
// guarded against silently accepting a genuine qualifier mismatch.)
// The aliased qualifier `lexer.TokA` and the basename `tokens.Tok` now
// both denote ./tokens, so the program is valid and type-checks.
func TestCrossModuleVariantPatternAliasQualifier(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tokens.fern"), []byte(crossModuleVariantTokens), 0o644); err != nil {
		t.Fatalf("write tokens.fern: %v", err)
	}
	src := `
import "./tokens" as lexer;
import "./tokens";

function main(): i32 {
    let t: tokens.Tok = tokens.make_a();
    match (t) {
        lexer.TokA(a) => { return a.x; },
        lexer.TokB(b) => { return b.y; }
    }
    return 99;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.fern"), []byte(src), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("alias + basename import of the same module should load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Errorf("alias qualifier `lexer.TokA` should resolve to ./tokens like the basename does; got: %v", err)
	}
}
