package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Arrow lambdas `(params): R => expr` are concise anonymous functions
// (desugared to `(params): R => { return expr; }`). Exercised here as
// closures passed to (non-generic, so every backend's helper handles them
// without the monomorph pass) higher-order functions: apply(10, n=>n*2)=20
// + combine(3,4,(x,y)=>x+y)=7 → 27. See #2701.
const arrowLambdaSrc = `function apply(x: i32, f: (i32) => i32): i32 { return f(x); }
function combine(a: i32, b: i32, f: (i32, i32) => i32): i32 { return f(a, b); }
function main(): i32 {
    var d: i32 = apply(10, (n: i32): i32 => n * 2);
    var s: i32 = combine(3, 4, (x: i32, y: i32): i32 => x + y);
    return d + s;
}
`

func TestInterpArrowLambda(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(arrowLambdaSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 27 {
		t.Errorf("exit = %d, want 27\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64ArrowLambda(t *testing.T) {
	out, code := compileAndRunX86_64(t, arrowLambdaSrc)
	if code != 27 {
		t.Errorf("exit = %d, want 27\n%s", code, out)
	}
}

func TestArm64ArrowLambda(t *testing.T) {
	out, code := compileAndRunArm64(t, arrowLambdaSrc)
	if code != 27 {
		t.Errorf("exit = %d, want 27\n%s", code, out)
	}
}

func TestWASMArrowLambda(t *testing.T) {
	if code := runWasm(t, arrowLambdaSrc); code != 27 {
		t.Errorf("wasm exit = %d, want 27", code)
	}
}
