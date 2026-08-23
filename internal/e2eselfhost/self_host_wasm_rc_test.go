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
// inc/dec call sites ride on it in later slices.
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

// TestSelfHostWasmMapValidWAT is a regression guard for the self-host wasm
// AST-path bug where a map-using module emitted `call_indirect (type $fn1/$fn2)`
// in the map hash/eq helpers (map_helpers, the derived struct/enum-key Hash/Eq
// dispatch, #2671) WITHOUT declaring those signature types or a function table —
// so the module failed wasm validation (and traps / mis-exits at run time).
// The type+table section was gated on `lam_ctr > 0` (closures only), so:
//   - a map program with NO closures declared no table and no $fn types; and
//   - even a map program WITH closures declared only $clos<N>, never $fn1/$fn2.
//
// Both cases now emit $fn1/$fn2 and a (>=1-slot) table whenever the module uses
// maps — the AST-path mirror of wasm_ir.fn_support_section (the IR path). Each
// case is success-coded (return 0 iff every map op is correct), so a non-zero
// exit (incl. a validation/trap exit) fails the test.
func TestSelfHostWasmMapValidWAT(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm map-WAT e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	cases := []struct {
		name string
		src  string
	}{
		// Map literal, NO closures — the primary case (module declared no table,
		// no $fn types, so map_helpers' call_indirect was undefined).
		{"map-literal-no-closure", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; var ks: i32[] = m.keys(); if (ks.len() != 2) { return 1; } if (m.get_or(1, 0) != 10) { return 2; } if (m.get_or(2, 0) != 20) { return 3; } return 0; }"},
		// map_new + inserts, no closures — same shape via the explicit builder API.
		{"map-new-no-closure", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(1, 10); m = m.insert(2, 20); if (m.len() != 2) { return 1; } if (!m.has(1)) { return 2; } if (m.get_or(2, 0) != 20) { return 3; } return 0; }"},
		// Map AND a closure (call_indirect $clos) in the same module — before the
		// fix the $clos block emitted only $clos<N>, never $fn1/$fn2, so this was
		// invalid too. Both dispatch-type families must now coexist.
		{"map-plus-closure", "function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var m = Map { 5: 50, 6: 60 }; var g: (i32) => i32 = (n: i32) => n + 1; var r = apply(g, 7); if (r != 8) { return 1; } if (m.len() != 2) { return 2; } if (m.get_or(5, 0) != 50) { return 3; } return 0; }"},
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
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s: wasm exited %d, want 0 (valid WAT + correct map ops)\n--- WAT ---\n%s", tc.name, code, wat)
			}
		})
	}
}

// TestSelfHostRcFreeWasm exercises the wasm Perceus FREE flip: arrays are
// now reclaimed via $__fern_arr_dec into a size-class freelist, and
// $__fern_alloc pops a freed block before bumping. Reuse is observable as
// pointer equality — after freeing an array, a same-size allocation gets
// the very same block back. Also checks that a churn (many build/discard
// cycles) runs cleanly and that array values survive the free machinery.
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
		// Freeing an array and allocating a same-size one reuses the block
		// (the freelist pop returns the just-freed block → equal data ptr).
		{"freelist-reuse", "function main(): i32 { var a: i32[] = [1, 2, 3]; __fern_arr_dec(a); var b: i32[] = [9, 9, 9]; if (a == b) { return 7; } return 0; }", 7},
		// Different size class is NOT reused (no false aliasing).
		{"freelist-distinct-class", "function main(): i32 { var a: i32[] = [1, 2, 3]; __fern_arr_dec(a); var b: i32[] = [1, 2, 3, 4, 5, 6, 7, 8]; if (a == b) { return 1; } return 7; }", 7},
		// A build/discard churn far exceeding the heap completes (reuse keeps
		// memory bounded) and stays value-correct + detector-clean.
		{"reclaim-churn", "function work(n: i32): i32 { var xs: i32[] = []; var i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs[n - 1]; } function main(): i32 { var k = 0; var s = 0; while (k < 100000) { s = work(64); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 0},
		// A LARGE array (> 64 KiB block) is recycled too — the freelist cap
		// (65536 classes / 512 KiB) matches the asm backends. gen(20000) has
		// cap 32768 → a ~128 KiB block; freeing it and rebuilding the same
		// size pops that block back (equal data pointer).
		{"reclaim-large-block", "function gen(n: i32): i32[] { var xs: i32[] = []; var i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { var a: i32[] = gen(20000); __fern_arr_dec(a); var b: i32[] = gen(20000); if (a == b) { return 7; } return 0; }", 7},
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
		{"freshbuiltin-mapkeys-swept", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; var ks: i32[] = m.keys(); return ks.len() + __rc_underflow_count(); }", 2},
		// A .values() snapshot re-bound each loop iteration: fresh + swept +
		// per-iteration dec-on-overwrite, value-correct + detector clean.
		{"freshbuiltin-values-loop", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; var s = 0; var k = 0; while (k < 100) { var vs: i32[] = m.values(); s = s + vs[0]; k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 6},
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

// TestSelfHostRcConstructWasm exercises the wasm Perceus construction-
// store inc: storing an EXISTING array reference into a struct field or
// an array-of-arrays element retains the buffer (the container co-owns it
// alongside the source local). Each program inspects the stored array's
// rc (now > 1 => not unique) and asserts the over-release detector stays
// clean — the inc balances the exit sweep's dec of both the source local
// and (eventually, once struct/array free lands) the container. Free is
// still OFF; this is the soundness prerequisite that lets a later slice
// free arrays without dangling a stored reference.
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
		// Array stored into a struct field: source local is no longer unique
		// (the struct co-owns it), values intact, detector clean.
		{"struct-field-retained", "struct H { items: i32[] } function main(): i32 { var xs: i32[] = [1, 2, 3]; var h = H { items: xs }; var u = __fern_rc_is_unique(xs); return u + h.items[2] + __rc_underflow_count(); }", 3},
		// Array-of-arrays: each element array is retained by the outer array.
		{"array-of-arrays-retained", "function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = [3, 4]; var both = [a, b]; var ua = __fern_rc_is_unique(a); return ua + both[0][1] + both[1][0] + __rc_underflow_count(); }", 5},
		// Fresh literal stored (no source local): moved into the struct, NOT
		// inc'd; still reads back correctly, detector clean.
		{"struct-field-fresh-move", "struct H { items: i32[] } function main(): i32 { var h = H { items: [9, 8, 7] }; return h.items[0] + __rc_underflow_count(); }", 9},
		// Array stored into a tuple element is retained.
		{"tuple-elem-retained", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var t = (xs, 99); var u = __fern_rc_is_unique(xs); return u + t.0[2] + __rc_underflow_count(); }", 3},
		// Array wrapped in Some(...) (Option payload) is retained.
		{"option-payload-retained", "function main(): i32 { var xs: i32[] = [4, 5, 6]; var o = Some(xs); var u = __fern_rc_is_unique(xs); return u + xs[1] + __rc_underflow_count(); }", 5},
		// Array wrapped in Ok(...) (Result payload) is retained.
		{"result-payload-retained", "function main(): i32 { var xs: i32[] = [7, 8]; var r = Ok(xs); var u = __fern_rc_is_unique(xs); return u + xs[0] + __rc_underflow_count(); }", 7},
		// struct-update base-copy: a non-overridden array field copied from
		// the base struct is retained (both structs co-own it).
		{"struct-update-base-copy-retained", "struct H { items: i32[], n: i32 } function main(): i32 { var xs: i32[] = [1, 2]; var h = H { items: xs, n: 0 }; var h2 = H { ...h, n: 5 }; var u = __fern_rc_is_unique(xs); return u + h2.items[1] + h2.n + __rc_underflow_count(); }", 7},
		// User enum variant with an array payload is retained.
		{"enum-variant-payload-retained", "enum Box { Arr(i32[]), Empty } function main(): i32 { var xs: i32[] = [3, 4, 5]; var b = Arr(xs); var u = __fern_rc_is_unique(xs); return u + xs[2] + __rc_underflow_count(); }", 5},
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

// TestSelfHostRcArrayLayoutWasm proves the wasm array-layout migration
// end to end: array blocks now reserve an rc word at [data-8] (via
// $__fern_arr_box), initialised to 1 for a fresh owner, while every
// a-relative access (len / cap / elems) is unchanged. A real array
// literal (and an append-grown array) is passed straight to the rc
// intrinsics — fresh => unique (rc==1); after an inc => not unique
// (rc==2); inc+dec restores uniqueness; and the over-release detector
// stays clean. RC is otherwise inert here (no inc/free wired into array
// sites yet — that rides on this layout in the next slices).
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
		// Fresh array literal: rc == 1 => unique.
		{"literal-fresh-unique", "function main(): i32 { var xs: i32[] = [10, 20, 30]; return __fern_rc_is_unique(xs); }", 1},
		// After an inc: rc == 2 => not unique.
		{"literal-after-inc-not-unique", "function main(): i32 { var xs: i32[] = [1, 2, 3]; __fern_rc_inc(xs); return __fern_rc_is_unique(xs); }", 0},
		// inc then dec restores rc == 1 => unique again.
		{"literal-inc-dec-unique", "function main(): i32 { var xs: i32[] = [1, 2]; __fern_rc_inc(xs); __fern_rc_dec(xs); return __fern_rc_is_unique(xs); }", 1},
		// Element values still read correctly through the shifted data ptr.
		{"elems-intact-after-layout", "function main(): i32 { var xs: i32[] = [7, 8, 9]; return xs[0] + xs[2] + xs.len(); }", 19},
		// An append-grown array is also rc-boxed (via $__fern_arr_box on grow).
		{"appended-array-boxed", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 10) { xs = xs.append(i); i = i + 1; } return __fern_rc_is_unique(xs) + xs[9]; }", 10},
		// Balanced inc/dec on a real array: detector clean (0).
		{"detector-clean", "function main(): i32 { var xs: i32[] = [1, 2, 3]; __fern_rc_inc(xs); __fern_rc_dec(xs); return __rc_underflow_count(); }", 0},
		// Over-release a real array (dec past rc==0): detector fires (1).
		{"detector-over-release", "function main(): i32 { var xs: i32[] = [1, 2, 3]; __fern_rc_dec(xs); __fern_rc_dec(xs); return __rc_underflow_count(); }", 1},
		// Peripheral producers are rc-boxed too (uniform layout): a
		// random_bytes() array and a map .values() snapshot both carry rc==1.
		{"random-bytes-boxed", "function main(): i32 { var b: u8[] = random_bytes(4); return __fern_rc_is_unique(b); }", 1},
		{"map-values-boxed", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; var vs = m.values(); return __fern_rc_is_unique(vs) + vs.len(); }", 3},
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
