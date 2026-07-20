package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The Fold pass simplifies a br_if whose condition is a compile-time
// constant (the loop / break / continue shape), mirroring its existing
// constant-if pruning. `while (true)` drops its exit br_if entirely (the
// loop body's `break` becomes the only exit); `while (false)` turns the
// exit br_if into an unconditional OpBr so the loop exits at the top.
// Both must keep the same observable behaviour on every backend.
//
// count_true iterates until i reaches 5 via the break (exercises the
// constant-false / never-taken br_if that Fold drops); count_false never
// runs its body (exercises the constant-true / always-taken br_if that
// Fold rewrites to OpBr). 5 + 0 = 5.
const constBrIfFoldSrc = `function count_true(): i32 {
    var i: i32 = 0;
    while (true) {
        i = i + 1;
        if (i >= 5) { break; }
    }
    return i;
}
function count_false(): i32 {
    var i: i32 = 0;
    while (false) { i = i + 1; }
    return i;
}
function main(): i32 {
    return count_true() + count_false();
}
`

func TestInterpConstBrIfFold(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(constBrIfFoldSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 5 {
		t.Errorf("exit = %d, want 5\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64ConstBrIfFold(t *testing.T) {
	out, code := compileAndRunX86_64(t, constBrIfFoldSrc)
	if code != 5 {
		t.Errorf("exit = %d, want 5\n%s", code, out)
	}
}

func TestArm64ConstBrIfFold(t *testing.T) {
	out, code := compileAndRunArm64(t, constBrIfFoldSrc)
	if code != 5 {
		t.Errorf("exit = %d, want 5\n%s", code, out)
	}
}

func TestWASMConstBrIfFold(t *testing.T) {
	if code := runWasm(t, constBrIfFoldSrc); code != 5 {
		t.Errorf("wasm exit = %d, want 5", code)
	}
}
