package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// nestedFnValueIRCases exercise a NESTED named function (which desugars to a
// var-bound lambda) that is used as a VALUE — returned as an enum payload, or
// passed to a higher-order function — rather than only being called locally.
//
// Before this slice, only two fn-value-in-payload shapes routed the self-host IR
// path: a bare top-level fn name (`Pending(41, step)`, slice 5) and a capturing
// lambda LITERAL written directly in payload position (slice 5b). A nested fn
// BOUND to a local and then referenced by name (`function resume(..){..}; return
// Pend(c, resume);`) bailed to the AST emitter: the var-bound lambda was left for
// closure_lift_one, which only lifts call-only closures, so a value-used binding
// was never boxed and the module fell back to AST.
//
// Two irlower changes fix it: (1) lambda_captures only treats an ENCLOSING LOCAL
// as a capture (enum constructors / I/O builtins that are free in the body are no
// longer mis-captured, which had made make_clo_func decline), and (2) a
// var-bound lambda USED AS A VALUE is env-boxed into the uniform `__mkclo$` box
// every fn-value carries — gated on a value-use analysis so a call-ONLY binding
// is still left to closure_lift_one's cheaper direct-call lift (boxing it would
// regress closure-calls-closure). Captures must be i32 (or absent); a string /
// pointer capture still declines and the module bails to AST (a later slice).
var nestedFnValueIRCases = []struct {
	name string
	src  string
}{
	// A non-capturing nested fn returned as the Pending continuation; k(1) -> 1+41.
	{"nocap_payload", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32): Fut {
    function resume(woken: i32): Fut { return Rdy(woken + 41); }
    return Pend(c, resume);
}
function main(): i32 {
    var f: Fut = drain(0);
    match (f) {
        Rdy(v) => { return v; },
        Pend(fd, k) => { var r: Fut = k(1); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } }
    }
    return 99;
}`},
	// An i32-capturing nested fn (`resume` captures `acc`) as the payload; k(1) ->
	// 1 + 41.
	{"i32cap_payload", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, acc: i32): Fut {
    function resume(woken: i32): Fut { return Rdy(acc + woken); }
    return Pend(c, resume);
}
function main(): i32 {
    var f: Fut = drain(0, 41);
    match (f) {
        Rdy(v) => { return v; },
        Pend(fd, k) => { var r: Fut = k(1); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } }
    }
    return 99;
}`},
	// A nested fn capturing TWO i32s as the payload; k(1) -> 1 + 30 + 11.
	{"multicap_payload", `enum Fut { Rdy(i32), Pend(i32, (i32) => Fut) }
function drain(c: i32, a: i32, b: i32): Fut {
    function resume(woken: i32): Fut { return Rdy(woken + a + b); }
    return Pend(c, resume);
}
function main(): i32 {
    var f: Fut = drain(0, 30, 11);
    match (f) {
        Rdy(v) => { return v; },
        Pend(fd, k) => { var r: Fut = k(1); match (r) { Rdy(v2) => { return v2; }, Pend(a, b) => { return 0; } } }
    }
    return 99;
}`},
	// A capturing fn-value passed to a higher-order function (not via an enum):
	// `apply(add, 9)` with `add` capturing `base` -> 33 + 9.
	{"hof_arg", `function apply(g: (i32) => i32, n: i32): i32 { return g(n); }
function mk(base: i32): i32 {
    var add = function(x: i32): i32 { return x + base; };
    return apply(add, 9);
}
function main(): i32 { return mk(33); }`},
}

// TestSelfHostNestedFnValueIRX86_64 builds the self-host asm_run + path-probe
// drivers and, for each program, asserts it routes the IR path (probe == "ir")
// and runs to the interpreter oracle (all exit 42 here). x86-64.
func TestSelfHostNestedFnValueIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "probe")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range nestedFnValueIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(src))

			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%q routed through %q path, want \"ir\" (value-used nested fn bailed lower_func)", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "nested_fn_value_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%q exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
