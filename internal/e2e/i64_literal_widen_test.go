package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// unannotatedBigLiteralProgram pins the #3676 semantics: an UNANNOTATED integer
// literal that doesn't fit i32 (`2147483648`, one past i32 max) defaults to i64
// rather than being silently truncated. i32 is the default int, so `var x = 5`
// stays i32 — but a written-out constant past i32 range has no valid i32 reading,
// so it widens to i64 (option 2 of the issue). Before the fix, native x86-64
// truncated the literal to INT_MIN (so `x < 0` was true → exit 1) while the AST
// interpreter and the self-host IR kept it wide (exit 0); native now widens too,
// so all paths agree on exit 0. Arithmetic on an i32 value still wraps at 32
// bits (#3581) — only the bare literal's own type widens.
const unannotatedBigLiteralProgram = `
function main(): i32 {
  var x = 2147483648;        // one past i32 max, no annotation → i64
  if (x < 0) { return 1; }   // i64 2147483648 is positive → false
  return 0;
}
`

// bigLiteralArithmeticProgram shows the widened literal carries its full value
// into a later i64 op: 5000000000 / 1000000000 == 5 (would be garbage if
// truncated to i32).
const bigLiteralArithmeticProgram = `
function main(): i32 {
  var x = 5000000000;
  return (x / 1000000000) as i32;
}
`

func TestInterpUnannotatedBigLiteralWidens(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(unannotatedBigLiteralProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("interp exit = %d, want 0 (literal widens to i64, stays positive)\nstderr: %s", code, errb.String())
	}
}

func TestX86_64UnannotatedBigLiteralWidens(t *testing.T) {
	if _, code := compileAndRunX86_64(t, unannotatedBigLiteralProgram); code != 0 {
		t.Errorf("x86-64 big-literal widen: exit = %d, want 0", code)
	}
	if _, code := compileAndRunX86_64(t, bigLiteralArithmeticProgram); code != 5 {
		t.Errorf("x86-64 big-literal arithmetic: exit = %d, want 5", code)
	}
}

func TestArm64UnannotatedBigLiteralWidens(t *testing.T) {
	if _, code := compileAndRunArm64(t, unannotatedBigLiteralProgram); code != 0 {
		t.Errorf("arm64 big-literal widen: exit = %d, want 0", code)
	}
	if _, code := compileAndRunArm64(t, bigLiteralArithmeticProgram); code != 5 {
		t.Errorf("arm64 big-literal arithmetic: exit = %d, want 5", code)
	}
}

func TestWASMUnannotatedBigLiteralWidens(t *testing.T) {
	if code := runWasm(t, unannotatedBigLiteralProgram); code != 0 {
		t.Errorf("wasm big-literal widen: exit = %d, want 0", code)
	}
	if code := runWasm(t, bigLiteralArithmeticProgram); code != 5 {
		t.Errorf("wasm big-literal arithmetic: exit = %d, want 5", code)
	}
}
