package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Fold combines an additive constant chain across a non-constant operand
// even when + and - mix: `(x ±a c1) ±b c2` folds to a single `x + net`.
// `x - 1 - 2` → x-3, `x + 8 - 4` → x+4, `x - 8 + 4` → x-4, and
// `x + 5 - 5` → x (net 0 drops via strength reduction). The folded net
// must compute the same result on every backend.
//
// suba(10)=7, addsub(10)=14, subadd(20)=16, netzero(9)=9, chain(4)=6
// → 7+14+16+9+6 = 52.
const additiveChainFoldSrc = `function suba(x: i32): i32 { return x - 1 - 2; }
function addsub(x: i32): i32 { return x + 8 - 4; }
function subadd(x: i32): i32 { return x - 8 + 4; }
function netzero(x: i32): i32 { return x + 5 - 5; }
function chain(x: i32): i32 { return x + 1 - 2 + 3; }
function main(): i32 {
    return suba(10) + addsub(10) + subadd(20) + netzero(9) + chain(4);
}
`

func TestInterpAdditiveChainFold(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(additiveChainFoldSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 52 {
		t.Errorf("exit = %d, want 52\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64AdditiveChainFold(t *testing.T) {
	out, code := compileAndRunX86_64(t, additiveChainFoldSrc)
	if code != 52 {
		t.Errorf("exit = %d, want 52\n%s", code, out)
	}
}

func TestArm64AdditiveChainFold(t *testing.T) {
	out, code := compileAndRunArm64(t, additiveChainFoldSrc)
	if code != 52 {
		t.Errorf("exit = %d, want 52\n%s", code, out)
	}
}

func TestWASMAdditiveChainFold(t *testing.T) {
	if code := runWasm(t, additiveChainFoldSrc); code != 52 {
		t.Errorf("wasm exit = %d, want 52", code)
	}
}
