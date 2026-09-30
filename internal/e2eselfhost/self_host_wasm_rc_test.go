package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcRuntimeWasm — the Perceus RC runtime helpers via the
// self-hosted wasm backend (examples/self_host/wasm_ir.fern). The wasm32
// mirror of TestSelfHostRcRuntimeX86_64 / ...Arm64: the rc word is an i32
// at [data-8], the helpers (__fern_rc_inc / __fern_rc_dec /
// __fern_rc_is_unique / __fern_rc_underflow_count) plus the raw-memory
// pokes (__alloc / __load_i32 / __store_i32) are emitted into the wasm
// module (gated on use), and a program hand-builds an rc-headered object
// via __alloc + __store_i32 to exercise them directly. This is the
// additive Phase-0c foundation for wasm RC — array layout migration +
// inc/dec call sites build on it in later slices.
//
// Reuses the shared rcRuntimeCases (defined in self_host_rc_runtime_test.go):
// the `return <expr>;` result becomes the wasm proc_exit code, same as the
// asm backends, so the expected exit codes carry over unchanged.
func TestSelfHostRcRuntimeWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm RC e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	for _, tc := range rcRuntimeCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}

// TestSelfHostRcFreeWasm checks that a build/discard churn far exceeding the
// heap runs cleanly (freed arrays are reused) and that array values survive
// the free machinery.
func TestSelfHostRcFreeWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm RC free e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// A build/discard churn far exceeding the heap completes (reuse keeps
		// memory bounded) and stays value-correct + detector-clean.
		{"reclaim-churn", "function work(n: i32): i32 { var xs: i32[] = []; var i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs[n - 1]; } function main(): i32 { var k = 0; var s = 0; while (k < 100000) { s = work(64); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}

// TestSelfHostRcCallResultWasm exercises reclamation of CALL-RESULT array
// locals (`var x = build()`): counted and released at function exit rather
// than never swept, because the callee is a user function
// declared to return an array (so its StmtReturn applies return-retain).
// Each program asserts the value AND a clean over-release detector — the
// crucial case being a callee that returns a BORROWED param (return-retain
// inc'd it, so the caller can sweep both source and result without a
// double-free). Method calls / in-place receivers are deliberately NOT
// swept, so a self-append result never double-frees.
func TestSelfHostRcCallResultWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm RC call-result e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	const genFn = "function gen(n: i32): i32[] { var xs: i32[] = []; var i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } "
	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Two call-result locals: both swept (freed) at exit, detector clean.
		{"callresult-balanced", genFn + "function main(): i32 { var a: i32[] = gen(5); var b: i32[] = gen(7); return a[4] + b[6] + __rc_underflow_count(); }", 10},
		// Aliasing a call result: the alias is inc'd, both swept, balanced.
		{"callresult-aliased", genFn + "function main(): i32 { var a: i32[] = gen(5); var c = a; return a[0] + c[4] + __rc_underflow_count(); }", 4},
		// Callee returns a BORROWED param: return-retain protects the buffer
		// so the caller sweeping BOTH source and result is not a double-free.
		{"callresult-borrowed-return", "function pick(xs: i32[]): i32[] { return xs; } function main(): i32 { var src: i32[] = [1, 2, 3]; var got: i32[] = pick(src); return got[1] + src[0] + __rc_underflow_count(); }", 3},
		// A self-append (method call, in-place receiver) result is NOT swept,
		// so it never double-frees the receiver's buffer.
		{"self-append-not-double-freed", "function main(): i32 { var xs: i32[] = [1, 2]; var ys = xs.append(3); return ys[2] + __rc_underflow_count(); }", 3},
		// Fresh-array builtins/methods bound to a local are reclaimed too.
		{"freshbuiltin-random-swept", "function main(): i32 { var b: u8[] = random_bytes(4); return b.len() + __rc_underflow_count(); }", 4},
		// A declared-array local inited from a USER method returning an array
		// is counted/swept (the method's return-retain makes it safe).
		{"method-result-swept", "struct Box { n: i32 } function (b: Box) make(): i32[] { var xs: i32[] = []; var i = 0; while (i < b.n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { var box = Box { n: 5 }; var r: i32[] = box.make(); return r[4] + __rc_underflow_count(); }", 4},
		// Same, re-bound each loop iteration: per-iteration release, clean.
		{"method-result-loop", "struct Box { n: i32 } function (b: Box) make(): i32[] { var xs: i32[] = []; var i = 0; while (i < b.n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { var box = Box { n: 6 }; var s = 0; var k = 0; while (k < 50) { var r: i32[] = box.make(); s = s + r[5]; k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}

// TestSelfHostRcConstructWasm checks that a fresh array literal stored into a
// struct field is moved rather than retained: it reads back correctly and the
// over-release detector stays clean.
func TestSelfHostRcConstructWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm RC construction e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Fresh literal stored (no source local): moved into the struct, NOT
		// inc'd; still reads back correctly, detector clean.
		{"struct-field-fresh-move", "struct H { items: i32[] } function main(): i32 { var h = H { items: [9, 8, 7] }; return h.items[0] + __rc_underflow_count(); }", 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}

// TestSelfHostRcCountingWasm exercises the wasm Perceus array counting
// milestone (inc-on-alias + function-exit release sweep, free still OFF)
// on NORMAL array programs — no manual rc intrinsic calls. Each program
// returns a computed value plus __rc_underflow_count(): the value
// proves array semantics are unchanged (free off), and the detector being
// 0 proves the inc retains and the exit-sweep decs BALANCE across aliases,
// append loops, multiple calls, and borrowed array params (callers' arrays
// survive, callees don't release them). This is the detector-cleanliness
// gate that must hold before free is flipped on.
func TestSelfHostRcCountingWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm RC counting e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// inc-on-alias + sweep balance: one alias, detector clean.
		{"alias-balanced", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys = xs; return ys[1] + xs[0] + __rc_underflow_count(); }", 3},
		// Multiple aliases of the same buffer: each inc, each swept.
		{"multi-alias-balanced", "function main(): i32 { var xs: i32[] = [10, 20]; var a = xs; var b = xs; return a[0] + b[1] + __rc_underflow_count(); }", 30},
		// Append-built array: owned, swept once, detector clean.
		{"append-loop-balanced", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 10) { xs = xs.append(i); i = i + 1; } return xs[9] + __rc_underflow_count(); }", 9},
		// A helper that aliases + returns; called repeatedly, all balanced.
		{"calls-balanced", "function f(): i32 { var xs: i32[] = [1, 2, 3]; var ys = xs; return ys[0]; } function main(): i32 { var r = f(); var s = f(); return r + s + __rc_underflow_count(); }", 2},
		// Borrowed array param: the callee aliases it (inc+sweep balanced)
		// but does NOT release the caller's buffer, which stays usable.
		{"borrowed-param-balanced", "function g(a: i32[]): i32 { var b = a; return b[0]; } function main(): i32 { var xs: i32[] = [7, 8]; var r = g(xs); return r + xs[0] + __rc_underflow_count(); }", 14},
		// Array local declared in a not-taken branch: zero-inited slot, the
		// sweep's dec(0) is a no-op, detector clean.
		{"branch-local-balanced", "function main(): i32 { var xs: i32[] = [5, 6]; if (xs[0] > 100) { var ys: i32[] = [1, 2]; return ys[0] + __rc_underflow_count(); } return xs[1] + __rc_underflow_count(); }", 6},
		// A loop-local array re-bound each iteration is released per-iteration
		// (StmtVar cow-guarded dec-on-overwrite), not just at function exit —
		// 1000 rebinds stay value-correct and over-release-detector clean.
		{"loop-local-rebind-clean", "function gen(n: i32): i32[] { var xs: i32[] = []; var i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { var s = 0; var k = 0; while (k < 1000) { var r: i32[] = gen(8); s = s + r[7]; k = k + 1; } return (s % 100) + __rc_underflow_count(); }", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}

// TestSelfHostRcArrayLayoutWasm checks that array blocks, which reserve an rc
// word at [data-8], keep every a-relative access (len / elems) unchanged.
func TestSelfHostRcArrayLayoutWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm array-layout RC e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Element values still read correctly through the shifted data ptr.
		{"elems-intact-after-layout", "function main(): i32 { var xs: i32[] = [7, 8, 9]; return xs[0] + xs[2] + xs.len(); }", 19},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
