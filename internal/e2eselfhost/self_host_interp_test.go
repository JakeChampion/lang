package e2eselfhost

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

// Fourth self-host milestone after the lexer (#609), parser (#611 /
// #617) and checker (#619). `examples/self_host/interp.fern` is a
// tree-walking interpreter written in lang — it imports `./lexer`
// and `./parser`, evaluates the Stmt[] tree from
// `parser.parse_program(toks)`, and produces a runtime Value
// (VInt / VBool / VString / VErr). This completes the
// lexer → parser → interp vertical slice in lang: the port can
// now actually *run* lang programs end-to-end, from a thin
// self-validating driver.
//
// Scope (matches the parser's): i32 / bool / string values, var
// binding via a flat env, arithmetic / comparison / logical /
// string-concat ops over the parser's op set, integer literal
// parsing from the lexer's TokNumber text.
//
// The .fern file's main() runs seven sub-checks: arithmetic +
// ident lookup, string concat, logical + equality, unary minus,
// integer division, division-by-zero VErr propagation, and
// undefined-identifier VErr propagation. Exit code 0 means every
// assertion passed; non-zero codes identify which arm failed.

func writeSelfHostInterpProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp.fern")
	return dir
}

func TestSelfHostInterpX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostInterpProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "interp.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
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
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port interp assertion %d failed", code)
	}
}

func TestSelfHostInterpArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeSelfHostInterpProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "interp.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
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
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port interp assertion %d failed", code)
	}
}
