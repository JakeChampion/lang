package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWideCaptureIR gates closures that capture an 8-byte scalar
// (i64 / u64 / f64) — #6046.
//
// The closure env box is an i32[], so cap_slot_ok takes pointer-shaped captures
// (every heap and code address in the -no-pie -static binary is 32-bit) but not
// a wide scalar. make_clo_func declined the whole closure, and since the AST
// emitters were deleted (#3457) that decline is a hard compile error: a closure
// over a duration, a file size or a timestamp did not compile at all.
//
// box_wide_captures snapshots each wide capture into a 1-element cell before the
// statement that builds the closure and re-binds the name inside the lambda
// (`var $wc$n: i64[] = [n];` … `var n: i64 = $wc$n[0];`), so what the lift passes
// see is an i64[] — pointer-shaped, which cap_slot_ok already accepted.
//
// Each case asserts the `-decide` route is "ir" AND the answer, because a
// regression here would not be silent in the same way as most: the module no
// longer falls back to a working AST emitter, it fails to build. The route
// assertion is what distinguishes "lowered on the IR path" from "lowered some
// other way" if a fallback is ever reintroduced.
//
// The i32 controls are the same programs with a narrow capture — they lowered
// before this change and must keep lowering, which is what isolates the width as
// the variable.
func TestSelfHostWideCaptureIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// outcome is the enum + trampoline the call-argument cases share: it forces
	// the lambda into a first-class position (an argument), which is the shape
	// that has no direct-call param-lift to fall back on.
	const outcome = "enum Outcome { Pass, Fail(i32) }\n" +
		"function run(f: () => Outcome): i32 {\n" +
		"    match (f()) { Pass => { return 0; }, Fail(n) => { return n; } }\n" +
		"}\n"

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Returned closure (hoist_escaping_closure), one wide capture, each width.
		{"escaping-i64-capture", "function make(n: i64): (i32) => i32 { return function(x: i32): i32 { return x + (n as i32); }; }\nfunction main(): i32 { var f = make(100 as i64); return f(5); }", 105},
		{"escaping-u64-capture", "function make(n: u64): (i32) => i32 { return function(x: i32): i32 { return x + (n as i32); }; }\nfunction main(): i32 { var f = make(100 as u64); return f(5); }", 105},
		{"escaping-f64-capture", "function make(d: f64): (i32) => i32 { return function(x: i32): i32 { return x + (d as i32); }; }\nfunction main(): i32 { var f = make(100.0); return f(5); }", 105},
		{"escaping-i32-capture-control", "function make(n: i32): (i32) => i32 { return function(x: i32): i32 { return x + n; }; }\nfunction main(): i32 { var f = make(100); return f(5); }", 105},

		// The capture is a LOCAL rather than a param, so the cell decl has to be
		// placed after the local's own decl, not at the top of the body.
		{"escaping-i64-local-capture", "function make(): (i32) => i32 { var n: i64 = 100 as i64; return function(x: i32): i32 { return x + (n as i32); }; }\nfunction main(): i32 { var f = make(); return f(5); }", 105},

		// Lambda as a CALL ARGUMENT — never bound to a local, so no direct-call
		// param-lift applies and the env box is the only route.
		{"callarg-i64-capture", outcome + "function check(): i32 { var b: i64 = 42 as i64; return run(function(): Outcome { return Fail(b as i32); }); }\nfunction main(): i32 { return check(); }", 42},
		{"callarg-i64-param-capture", outcome + "function check(b: i64): i32 { return run(function(): Outcome { return Fail(b as i32); }); }\nfunction main(): i32 { return check(42 as i64); }", 42},
		{"callarg-f64-capture", outcome + "function check(): i32 { var d: f64 = 10.5; return run(function(): Outcome { return Fail((d * 4.0) as i32); }); }\nfunction main(): i32 { return check(); }", 42},
		{"callarg-i32-capture-control", outcome + "function check(): i32 { var b: i32 = 42; return run(function(): Outcome { return Fail(b); }); }\nfunction main(): i32 { return check(); }", 42},

		// Two wide captures in one closure — the cells are distinct locals and the
		// env box carries both, in lambda_captures order.
		{"two-wide-captures", outcome + "function check(): i32 { var a: i64 = 20 as i64; var b: i64 = 22 as i64; return run(function(): Outcome { return Fail((a + b) as i32); }); }\nfunction main(): i32 { return check(); }", 42},

		// Wide and narrow and pointer-shaped together: only the wide one is
		// cellared, the rest ride the box slot directly as before.
		{"mixed-wide-narrow-pointer", outcome + "function check(): i32 { var a: i64 = 40 as i64; var n: i32 = 2; var s: string = \"xy\"; return run(function(): Outcome { return Fail((a as i32) + n + s.len() - 2); }); }\nfunction main(): i32 { return check(); }", 42},

		// The captured local is declared INSIDE an if-block, so its cell must be
		// emitted inside that block too — hoisting it to the top of the function
		// would reference the local before its decl.
		{"wide-capture-declared-in-if-block", outcome + "function check(k: i32): i32 {\n    if (k > 0) { var a: i64 = 42 as i64; return run(function(): Outcome { return Fail(a as i32); }); }\n    return 7;\n}\nfunction main(): i32 { return check(1); }", 42},

		// The capture is read more than once in the body — one cell, two reads of
		// the re-bound local, not two cells.
		{"wide-capture-read-twice", outcome + "function check(): i32 { var a: i64 = 21 as i64; return run(function(): Outcome { return Fail((a + a) as i32); }); }\nfunction main(): i32 { return check(); }", 42},

		// A wide capture the enclosing body REASSIGNS is deliberately NOT snapshot
		// into one of these cells: box_mutated_scalar_captures already boxes it
		// into a SHARED cell (#5394), which is what it needs — the closure has to
		// observe the post-write value. 42 proves the shared cell is still in
		// play; a creation-time snapshot would answer 1.
		{"reassigned-wide-capture-stays-shared", outcome + "function main(): i32 { var a: i64 = 1 as i64; var g = function(): Outcome { return Fail(a as i32); }; a = 42 as i64; return run(g); }", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\"", tc.name, route)
			}
			wat := runCapture(t, gcc, runner, driverBin, src)
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if werr := os.WriteFile(watPath, wat, 0o644); werr != nil {
				t.Fatalf("write wat: %v", werr)
			}
			cmd := exec.Command(wasmtime, "run", watPath)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n%s", tc.name, code, tc.exit, out)
			}
		})
	}
}
