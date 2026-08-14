package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// Post-check desugars inside a NESTED function body and a Lambda body.
//
// The checker builds these during checking and a post-check pass splices them
// into the AST — the checked-arithmetic block-expr (Binary.CheckedLowered) and
// the composite operator overloads (ArithCall / NegCall). That pass walks
// top-level function bodies, and it did not descend into a function declared
// INSIDE one, nor into an anonymous function expression.
//
// Neither the interpreter nor codegen can handle the un-desugared node, so the
// failure is a hard error rather than a wrong answer — but they fail at
// different times, which is what made the bug look narrower than it was.
// Compiling fails EAGERLY: the IR rejects the node at emit whether or not it
// would ever run ("ir: unsupported binary `+?`"). Interpreting fails only if
// the enclosing nested function is actually CALLED ("interp: `/?` on
// interp.Number and interp.Number not supported"). The generated seed that
// surfaced this never called its nested function on that input, so it looked
// like a compile-only problem; it is not.
//
// Hence every case below is reached through a call.
//
// Returns 0 when every case holds, else the index of the first failure.
const checkedArithNestedProgram = `
struct V { x: i32 }
function (a: V) add(b: V): V { return V { x: a.x + b.x }; }
function (a: V) neg(): V { return V { x: 0 - a.x }; }

function main(): i32 {
    // Checked arithmetic inside a nested function, every operator.
    function chk(): i32 {
        var n: i32 = 0;
        match (100 +? 5)  { Some(v) => { if (v != 105) { return 1; } }, None => { return 2; } }
        match (100 -? 5)  { Some(v) => { if (v != 95) { return 3; } }, None => { return 4; } }
        match (100 *? 5)  { Some(v) => { if (v != 500) { return 5; } }, None => { return 6; } }
        match (100 /? 5)  { Some(v) => { if (v != 20) { return 7; } }, None => { return 8; } }
        match (100 %? 7)  { Some(v) => { if (v != 2) { return 9; } }, None => { return 10; } }
        match (1 <<? 5)   { Some(v) => { if (v != 32) { return 11; } }, None => { return 12; } }
        match (256 >>? 2) { Some(v) => { if (v != 64) { return 13; } }, None => { return 14; } }
        // The None arms too, so the overflow half of the desugar runs here.
        match (2147483647 +? 1) { Some(v) => { return 15; }, None => {} }
        match (100 /? 0)        { Some(v) => { return 16; }, None => {} }
        match (1 <<? 32)        { Some(v) => { return 17; }, None => {} }
        return n;
    }
    var c: i32 = chk();
    if (c != 0) { return c; }

    // Composite operator overloads inside a nested function.
    function comp(): i32 {
        var p: V = V { x: 3 };
        var q: V = V { x: 4 };
        if ((p + q).x != 7) { return 20; }
        if ((-p).x != 0 - 3) { return 21; }
        return 0;
    }
    var d: i32 = comp();
    if (d != 0) { return d; }

    // The same desugars inside an anonymous function expression.
    var f: () => i32 = function (): i32 {
        match (100 /? 5) { Some(v) => { if (v != 20) { return 30; } }, None => { return 31; } }
        var p: V = V { x: 5 };
        var q: V = V { x: 6 };
        if ((p + q).x != 11) { return 32; }
        return 0;
    };
    var e: i32 = f();
    if (e != 0) { return e; }

    // The same desugars inside a loop { … } body. A loop is a statement kind
    // of its own rather than sugar over while (true), so a rewriter that lists
    // the loop forms by hand can omit it — and then the identical expression
    // compiles inside a while and is rejected inside a loop.
    var lp: i32 = 0;
    loop {
        match (100 /? 5) { Some(v) => { if (v != 20) { lp = 40; } }, None => { lp = 41; } }
        var p: V = V { x: 7 };
        var q: V = V { x: 8 };
        if ((p + q).x != 15) { lp = 42; }
        break;
    }
    if (lp != 0) { return lp; }

    return 0;
}
`

func TestInterpCheckedArithNested(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(checkedArithNestedProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("interp: exit = %d, want 0 (case %d failed)\nstdout: %s\nstderr: %s",
			code, code, out.String(), errb.String())
	}
}

func TestX86_64CheckedArithNested(t *testing.T) {
	if _, code := compileAndRunX86_64(t, checkedArithNestedProgram); code != 0 {
		t.Errorf("x86-64: exit = %d, want 0 (case %d failed)", code, code)
	}
}

func TestArm64CheckedArithNested(t *testing.T) {
	if _, code := compileAndRunArm64(t, checkedArithNestedProgram); code != 0 {
		t.Errorf("arm64: exit = %d, want 0 (case %d failed)", code, code)
	}
}

func TestWASMCheckedArithNested(t *testing.T) {
	if code := runWasm(t, checkedArithNestedProgram); code != 0 {
		t.Errorf("wasm: exit = %d, want 0 (case %d failed)", code, code)
	}
}
