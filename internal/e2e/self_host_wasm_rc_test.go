package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcRuntimeWasm — the Perceus RC runtime helpers via the
// self-hosted wasm backend (examples/self_host/wasm.fern). The wasm32
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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Array stored into a struct field: source local is no longer unique
		// (the struct co-owns it), values intact, detector clean.
		{"struct-field-retained", "struct H { items: i32[] } function main(): i32 { var xs: i32[] = [1, 2, 3]; var h = H { items: xs }; var u = __fern_rc_is_unique(xs); return u + h.items[2] + __fern_rc_underflow_count(); }", 3},
		// Array-of-arrays: each element array is retained by the outer array.
		{"array-of-arrays-retained", "function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = [3, 4]; var both = [a, b]; var ua = __fern_rc_is_unique(a); return ua + both[0][1] + both[1][0] + __fern_rc_underflow_count(); }", 5},
		// Fresh literal stored (no source local): moved into the struct, NOT
		// inc'd; still reads back correctly, detector clean.
		{"struct-field-fresh-move", "struct H { items: i32[] } function main(): i32 { var h = H { items: [9, 8, 7] }; return h.items[0] + __fern_rc_underflow_count(); }", 9},
		// Array stored into a tuple element is retained.
		{"tuple-elem-retained", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var t = (xs, 99); var u = __fern_rc_is_unique(xs); return u + t.0[2] + __fern_rc_underflow_count(); }", 3},
		// Array wrapped in Some(...) (Option payload) is retained.
		{"option-payload-retained", "function main(): i32 { var xs: i32[] = [4, 5, 6]; var o = Some(xs); var u = __fern_rc_is_unique(xs); return u + xs[1] + __fern_rc_underflow_count(); }", 5},
		// Array wrapped in Ok(...) (Result payload) is retained.
		{"result-payload-retained", "function main(): i32 { var xs: i32[] = [7, 8]; var r = Ok(xs); var u = __fern_rc_is_unique(xs); return u + xs[0] + __fern_rc_underflow_count(); }", 7},
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
// returns a computed value plus __fern_rc_underflow_count(): the value
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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// inc-on-alias + sweep balance: one alias, detector clean.
		{"alias-balanced", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys = xs; return ys[1] + xs[0] + __fern_rc_underflow_count(); }", 3},
		// Multiple aliases of the same buffer: each inc, each swept.
		{"multi-alias-balanced", "function main(): i32 { var xs: i32[] = [10, 20]; var a = xs; var b = xs; return a[0] + b[1] + __fern_rc_underflow_count(); }", 30},
		// Append-built array: owned, swept once, detector clean.
		{"append-loop-balanced", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 10) { xs = xs.append(i); i = i + 1; } return xs[9] + __fern_rc_underflow_count(); }", 9},
		// A helper that aliases + returns; called repeatedly, all balanced.
		{"calls-balanced", "function f(): i32 { var xs: i32[] = [1, 2, 3]; var ys = xs; return ys[0]; } function main(): i32 { var r = f(); var s = f(); return r + s + __fern_rc_underflow_count(); }", 2},
		// Borrowed array param: the callee aliases it (inc+sweep balanced)
		// but does NOT release the caller's buffer, which stays usable.
		{"borrowed-param-balanced", "function g(a: i32[]): i32 { var b = a; return b[0]; } function main(): i32 { var xs: i32[] = [7, 8]; var r = g(xs); return r + xs[0] + __fern_rc_underflow_count(); }", 14},
		// Array local declared in a not-taken branch: zero-inited slot, the
		// sweep's dec(0) is a no-op, detector clean.
		{"branch-local-balanced", "function main(): i32 { var xs: i32[] = [5, 6]; if (xs[0] > 100) { var ys: i32[] = [1, 2]; return ys[0] + __fern_rc_underflow_count(); } return xs[1] + __fern_rc_underflow_count(); }", 6},
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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
		{"detector-clean", "function main(): i32 { var xs: i32[] = [1, 2, 3]; __fern_rc_inc(xs); __fern_rc_dec(xs); return __fern_rc_underflow_count(); }", 0},
		// Over-release a real array (dec past rc==0): detector fires (1).
		{"detector-over-release", "function main(): i32 { var xs: i32[] = [1, 2, 3]; __fern_rc_dec(xs); __fern_rc_dec(xs); return __fern_rc_underflow_count(); }", 1},
		// Peripheral producers are rc-boxed too (uniform layout): a
		// random_bytes() array and a map .values() snapshot both carry rc==1.
		{"random-bytes-boxed", "function main(): i32 { var b: i32[] = random_bytes(4); return __fern_rc_is_unique(b); }", 1},
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
