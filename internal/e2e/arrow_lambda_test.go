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

// An arrow lambda annotating a TUPLE return type (#8706): `(i32, i32) =>` in
// return position is the tuple followed by the lambda's own arrow, not a
// function type. The identity shape returns its `(3, 4)` argument, so the
// caller's local and the result alias one tuple; exit 34 says both reads
// saw it intact.
const arrowLambdaTupleReturnSrc = `function main(): i32 {
    var id = (p: (i32, i32)): (i32, i32) => { return p; };
    var t = id((3, 4));
    return t.0 * 10 + t.1;
}
`

func TestInterpArrowLambdaTupleReturn(t *testing.T) {
	if code := runInterpByte(t, arrowLambdaTupleReturnSrc); code != 34 {
		t.Errorf("exit = %d, want 34", code)
	}
}

func TestX86_64ArrowLambdaTupleReturn(t *testing.T) {
	out, code := compileAndRunX86_64(t, arrowLambdaTupleReturnSrc)
	if code != 34 {
		t.Errorf("exit = %d, want 34\n%s", code, out)
	}
}

func TestArm64ArrowLambdaTupleReturn(t *testing.T) {
	out, code := compileAndRunArm64(t, arrowLambdaTupleReturnSrc)
	if code != 34 {
		t.Errorf("exit = %d, want 34\n%s", code, out)
	}
}

func TestWASMArrowLambdaTupleReturn(t *testing.T) {
	if code := runWasm(t, arrowLambdaTupleReturnSrc); code != 34 {
		t.Errorf("wasm exit = %d, want 34", code)
	}
}
