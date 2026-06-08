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
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
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
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
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
