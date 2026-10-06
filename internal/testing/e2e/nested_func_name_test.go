package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A nested function's name belongs to the block that declares it. The
// checker used to register it program-wide under the bare name, so the
// top-level `at(p, q)` here resolved to the nested `at(x)` at main's call
// and drew E004 (#9005). Every engine answers 3*4 + (1+1).
const nestedFuncNameSrc = `function outer(n: i32): i32 {
    function at(x: i32): i32 { return x + 1; }
    return at(n);
}
function at(p: i32, q: i32): i32 { return p * q; }
function main(): i32 { return at(3, 4) + outer(1); }
`

func TestInterpNestedFuncNameDoesNotShadowTopLevel(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(src, []byte(nestedFuncNameSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 14 {
		t.Errorf("exit = %d, want 14\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64NestedFuncNameDoesNotShadowTopLevel(t *testing.T) {
	out, code := compileAndRunX86_64(t, nestedFuncNameSrc)
	if code != 14 {
		t.Errorf("exit = %d, want 14\n%s", code, out)
	}
}

func TestArm64NestedFuncNameDoesNotShadowTopLevel(t *testing.T) {
	out, code := compileAndRunArm64(t, nestedFuncNameSrc)
	if code != 14 {
		t.Errorf("exit = %d, want 14\n%s", code, out)
	}
}

func TestWASMNestedFuncNameDoesNotShadowTopLevel(t *testing.T) {
	if code := runWasm(t, nestedFuncNameSrc); code != 14 {
		t.Errorf("wasm exit = %d, want 14", code)
	}
}
