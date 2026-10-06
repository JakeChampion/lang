package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestWasmSelfHostClosureArray guards closures stored in an array on the
// self-host wasm IR path. TestWasm*-named so test-e2e-wasm runs it under
// wasmtime in CI (TestSelfHostWasmRun is skipped there for lack of wasmtime).
//
// The bug it guards: closure-type was lost through an array load. A closure in
// a plain local works, but `let c = fns[0]; c()` (or `for f in fns { f() }`)
// mis-lowered the call to a raw call_indirect on the closure BOX pointer —
// using a heap address as a table index → out-of-bounds trap (exit 134).
// Fixed by tracking a closure-array slot (local_is_closurearr) and marking a
// local / loop var bound from such an element as a closure local, so its call
// unpacks the env box via the existing closure-call path.
func TestWasmSelfHostClosureArray(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm closure-array e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name   string
		source string
		exit   int
	}{
		// Index a closure array, bind, call.
		{"index-call", "function mk(s: i32): () => i32 { return (): i32 => { return s; }; } function main(): i32 { let fns: (() => i32)[] = [mk(42), mk(9)]; let c = fns[0]; return c(); }", 42},
		// Index the second element; the closure captures the call arg.
		{"index-arg", "function adder(n: i32): (i32) => i32 { return (x: i32): i32 => { return x + n; }; } function main(): i32 { let fs: ((i32) => i32)[] = [adder(10), adder(20)]; let g = fs[1]; return g(22); }", 42},
		// Iterate the array and call each closure, summing the results.
		{"for-each-call", "function mk(s: i32): () => i32 { return (): i32 => { return s; }; } function main(): i32 { let fns: (() => i32)[] = [mk(10), mk(20), mk(12)]; let s: i32 = 0; for f in fns { s = s + f(); } return s; }", 42},
		// Lambda elements (not just closure-returning calls), bound via locals.
		// (A *direct* `fns[0]()` index-call — no intermediate local — is a
		// separate follow-up sub-case and is not covered here.)
		{"lambda-elems", "function main(): i32 { let n: i32 = 5; let fns: (() => i32)[] = [(): i32 => { return n + 1; }, (): i32 => { return n * 2; }]; let a = fns[0]; let b = fns[1]; return a() + b(); }", 16},
		// A CAPTURING inline lambda in array-element position called DIRECTLY
		// (`fs[0](args)` — no intermediate local), the #2994 inline-closure lift:
		// lift_inline_closures hoists the body to `main$clo0(__env, x)` and replaces
		// the lambda with a `__mkclo$main$clo0(n)` env-box marker, so the array's
		// closurearr slot routes the direct index-call through the env-first path.
		// fs[0](7) = 7 + n = 7 + 35 = 42.
		{"inline-cap-direct-index", "function main(): i32 { let n: i32 = 35; let fs = [(x: i32): i32 => { return x + n; }]; return fs[0](7); }", 42},
		// Two distinct inline capturing lambdas in one array, each its own $cloN,
		// summed via a `for f in fs` loop. fs[0](10)=10+a=11, fs[1](10)=10*b=100.
		{"inline-cap-multi-forin", "function main(): i32 { let a: i32 = 1; let b: i32 = 10; let fs = [(x: i32): i32 => { return x + a; }, (x: i32): i32 => { return x * b; }]; let s = 0; for f in fs { s = s + f(10); } return s; }", 111},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
