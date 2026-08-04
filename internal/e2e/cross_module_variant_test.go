package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
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
// The two helpers below assemble a two-file project on disk so
// modload's import-resolution path is exercised end-to-end (rather
// than going through the single-source `compileAndRunX86_64` /
// `compileAndRunArm64` helpers).

const crossModuleVariantTokens = `pub struct TokA { x: i32 }
pub struct TokB { y: i32 }
pub type Tok = TokA | TokB;
pub function make_a(): Tok { return TokA { x: 5 }; }
pub function make_b(): Tok { return TokB { y: 17 }; }
`

const crossModuleVariantMain = `
import "./tokens";

function main(): i32 {
    var t1: tokens.Tok = tokens.make_a();
    var v1: i32 = 0;
    match (t1) {
        tokens.TokA(a) => { v1 = a.x; },
        tokens.TokB(b) => { v1 = b.y; }
    }
    var t2: tokens.Tok = tokens.make_b();
    var v2: i32 = 0;
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
	gcc, runner := x86_64Tooling(t)
	dir := writeCrossModuleVariantProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != 22 {
		t.Errorf("exit code: got %d, want 22", got)
	}
}

func TestCrossModuleVariantPatternArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeCrossModuleVariantProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != 22 {
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
    var t: tokens.Tok = tokens.make_a();
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
