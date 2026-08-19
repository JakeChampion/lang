package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// closureCallsClosureIRCases exercise a local closure whose body CALLS another
// local, capture-free closure (`var add = fn(a){…}; var twice = fn(a){
// add(add(a)) }`). `subst_fcall_expr` has to rewrite the hoisted `add`'s call
// sites that sit INSIDE `twice`'s body; otherwise `add` stays referenced, its
// lift declines, and the whole module bails to the
// AST emitter. Now `subst_fcall_expr` recurses into nested lambda bodies (for a
// capture-free hoist, no capture args to inject), and each lift round extends
// the global-fn set with the names hoisted so far — so a sibling lambda calling
// an already-hoisted `__lam_N` sees it as a global, not a capture it can't type.
// Both closures lift to direct calls to hoisted `__lam_N` functions on the IR
// path. Each case asserts the oracle exit code AND that lifting happened
// (`__lam_` in the emitted asm/wat). Exit codes are kept <= 120 (native) / <=
// 125 (WASI).
var closureCallsClosureIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// nested call `add(add(a))` — the canonical shape.
	{"nested", `function main(): i32 {
    var add = function(a: i32): i32 { return a + 10; };
    var twice = function(a: i32): i32 { return add(add(a)); };
    return twice(1);
}`, 21},
	// binary call `mk(a) + mk(a)` — two separate call sites in one expression.
	{"binary", `function main(): i32 {
    var mk = function(a: i32): i32 { return a * 3; };
    var combine = function(a: i32): i32 { return mk(a) + mk(a); };
    return combine(4);
}`, 24},
	// a three-deep chain: f -> dbl -> inc, each a capture-free local closure.
	{"chain", `function main(): i32 {
    var inc = function(a: i32): i32 { return a + 1; };
    var dbl = function(a: i32): i32 { return inc(a) * 2; };
    var f = function(a: i32): i32 { return dbl(a) + inc(a); };
    return f(3);
}`, 12},
	// control flow inside the calling closure (if + the called closure twice).
	{"if_body", `function main(): i32 {
    var pos = function(a: i32): i32 { if (a < 0) { return 0; } return a; };
    var clamp = function(a: i32): i32 { return pos(a) + pos(0 - 5); };
    return clamp(7);
}`, 7},
	// a loop in the calling closure calling the other closure each iteration.
	{"loop_body", `function main(): i32 {
    var sq = function(a: i32): i32 { return a * a; };
    var sumsq = function(n: i32): i32 { var s = 0; var i = 1; while (i <= n) { s = s + sq(i); i = i + 1; } return s; };
    return sumsq(3);
}`, 14},
	// the inner closure CAPTURES an outer variable (`add` captures `x`): the
	// injected capture arg flows through as the calling closure's own capture.
	{"capturing_inner", `function main(): i32 {
    var x = 10;
    var add = function(a: i32): i32 { return a + x; };
    var twice = function(a: i32): i32 { return add(a) + add(a); };
    return twice(1);
}`, 22},
	// the inner closure captures TWO variables.
	{"capturing_two", `function main(): i32 {
    var x = 3;
    var y = 7;
    var f = function(a: i32): i32 { return a * x + y; };
    var g = function(a: i32): i32 { return f(a) + f(a); };
    return g(2);
}`, 26},
	// two closures share a capture; a third calls both. (Return kept <= 125 for
	// the WASI proc_exit range — base 50 → (1+50)+(50+1) = 102.)
	{"shared_capture", `function main(): i32 {
    var base = 50;
    var f = function(a: i32): i32 { return a + base; };
    var g = function(a: i32): i32 { return base + a; };
    var h = function(a: i32): i32 { return f(a) + g(a); };
    return h(1);
}`, 102},
	// the calling closure both CALLS the capturing inner closure and uses the
	// captured variable directly.
	{"direct_and_call", `function main(): i32 {
    var x = 5;
    var add = function(a: i32): i32 { return a + x; };
    var combo = function(a: i32): i32 { return add(a) + x; };
    return combo(10);
}`, 20},
	// The called closure's binding SPELLS ITS TYPE OUT. That spelling is what
	// cap_type reports for the capture, and the injected param it becomes
	// carries no signature sidecars — so lower_func's "mark every fn param a
	// closure local" test, which reads the flat "fn" tag, did not fire and
	// `add` was dispatched as a raw table index instead of env-first. wasm
	// trapped (`undefined element`) and the register backends took a bus
	// error, both with the compiler reporting success.
	{"annotated_inner_called", `function main(): i32 {
    var add: (i32) => i32 = function(a: i32): i32 { return a + 10; };
    var twice = function(a: i32): i32 { return add(add(a)); };
    return twice(1);
}`, 21},
}

// TestSelfHostClosureCallsClosureX86IR builds the self-host asm_run driver and
// runs each program through it (Fern → x86-64 asm → native binary → exit code),
// asserting the oracle value and that the small IR path was taken (a bail to the
// ~35 KB AST runtime would be far larger).
func TestSelfHostClosureCallsClosureX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range closureCallsClosureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the closure-calls-closure module likely bailed to the AST runtime", len(asm))
			}
			if !strings.Contains(string(asm), "__lam_") {
				t.Fatalf("%q: no __lam_ hoisted lambda in asm — lifting did not happen", tc.name)
			}
			progBin := buildBin(t, gcc, dir, "ccc_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("closure-calls-closure %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostClosureCallsClosureWasmIR is the wasm sibling: the lift lives in
// the target-independent irlower, so the wasm IR backend gets it for free.
func TestSelfHostClosureCallsClosureWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host closure-calls-closure wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range closureCallsClosureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "ccc_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("closure-calls-closure wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
