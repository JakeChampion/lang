package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A negative i32 constant must be sign-extended into its 64-bit register
// slot, not zero-extended. i32 operand-stack values feed 64-bit-register
// arithmetic (index / slice / pointer math), so `x + (-2)` computed as
// `mov eax, -2 ; add rax, rcx` left rax = x + 0x00000000FFFFFFFE — a huge
// positive value once it flowed into a slice bound, tripping the
// bounds-check trap (exit 134) on x86-64. The i32 result was still correct
// (low 32 bits), so it only surfaced through 64-bit use.
//
// f("hello"): i = len + (0 - 2) = 3, then s[0 : i+1] = s[0:4] = "hell" → 4.
// This is the reduced form of modloader.parent_dir's `dir.len() - 1 - 1`
// (which folds to `len + (-2)`), the shape that crashed the self-host
// modload driver.
const negConstSextSrc = `function f(s: string): i32 {
    var i: i32 = s.len() + (0 - 2);
    var sub: str = s[0 : i + 1];
    return sub.len();
}
function main(): i32 {
    return f("hello");
}
`

func TestInterpNegConstSext(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(negConstSextSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 4 {
		t.Errorf("exit = %d, want 4\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64NegConstSext(t *testing.T) {
	out, code := compileAndRunX86_64(t, negConstSextSrc)
	if code != 4 {
		t.Errorf("exit = %d, want 4 (134 = the pre-fix bounds-trap bug)\n%s", code, out)
	}
}

func TestArm64NegConstSext(t *testing.T) {
	out, code := compileAndRunArm64(t, negConstSextSrc)
	if code != 4 {
		t.Errorf("exit = %d, want 4\n%s", code, out)
	}
}

func TestWASMNegConstSext(t *testing.T) {
	if code := runWasm(t, negConstSextSrc); code != 4 {
		t.Errorf("wasm exit = %d, want 4", code)
	}
}
