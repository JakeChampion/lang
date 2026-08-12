package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcClosureWasm proves Slice 1 of closure-env reclamation: a
// closure's env block is now rc-boxed (via $__fern_str_box, so table_idx@0 +
// captures@4+i*4 are unchanged) instead of raw-allocated, and an owned closure
// local (bound to a lambda literal) is FREED at function exit via the shared
// $__fern_arr_dec, reclaiming the env box rather than leaking it. A
// move-on-return closure is excluded from the sweep (handed to the caller).
// Captures still leak one level here (a later slice releases them); the box
// reclaim is sound on its own (detector 0). Cross-checks value + the
// over-release detector.
func TestSelfHostRcClosureWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("no wasmtime")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, _ := os.ReadFile(filepath.Join("../../examples/self_host", name))
		os.WriteFile(filepath.Join(dir, name), src, 0o644)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	cases := []struct {
		name string
		src  string
		exit int
	}{
		// closure box freed at exit; value-correct + detector 0.
		{"clos-box-freed", "function main(): i32 { var n: i32 = 5; var f = function (x: i32): i32 { return x + n; }; return f(37) + __rc_underflow_count(); }", 42},
		// multi-capture scalar closure freed; detector 0.
		{"clos-multi", "function main(): i32 { var a: i32 = 30; var b: i32 = 12; var f = function (): i32 { return a + b; }; return f() + __rc_underflow_count(); }", 42},
		// string capture: box freed (capture leaks one level — sound), value ok.
		{"clos-string-cap", "function main(): i32 { var s: string = \"hello\"; var f = function (): i32 { return s.len(); }; return f() + 37 + __rc_underflow_count(); }", 42},
		// churn: 200k scalar-capture closures built + dropped; boxes reclaim
		// each cycle (no OOM), detector 0 — proves real box free.
		{"clos-scalar-churn", "function mk(k: i32): i32 { var n: i32 = k; var f = function (x: i32): i32 { return x + n; }; return f(1); } function main(): i32 { var s = 0; var k = 0; while (k < 200000) { s = mk(k); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 3},
		// returned closure (move-on-return) excluded from sweep; caller's
		// binding (call init) not swept — no double free, value-correct.
		{"clos-return", "function adder(a: i32): (i32) => i32 { var f = function (b: i32): i32 { return a + b; }; return f; } function main(): i32 { var add10 = adder(10); var add20 = adder(20); return add10(5) + add20(7) + __rc_underflow_count(); }", 42},
		// Slice 2 — per-capture release. A heap STRING capture is released on the
		// closure's death (construction-inc'd at build, capture_kind-balanced),
		// value-correct + detector 0.
		{"cap-string-released", "function main(): i32 { var s: string = \"ab\" + \"cd\"; var f = function (): i32 { return s.len(); }; return f() + 38 + __rc_underflow_count(); }", 42},
		// Slice 2 churn: 200k closures each capturing a heap string; the capture
		// reclaims each cycle (no OOM) — without capture release this leaks.
		{"cap-string-churn", "function mk(): i32 { var s: string = \"ab\" + \"cd\"; var f = function (): i32 { return s.len(); }; return f(); } function main(): i32 { var k = 0; var n = 0; while (k < 200000) { n = mk(); k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 4},
		// Slice 2: an ARRAY capture (i32[]) reclaims each cycle across 200k.
		{"cap-array-churn", "function mk(): i32 { var xs: i32[] = [10, 20, 30]; var f = function (): i32 { return xs[1]; }; return f(); } function main(): i32 { var k = 0; var n = 0; while (k < 200000) { n = mk(); k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 6},
		// Slice 2: a STRUCT capture (deep) — the struct + its array field reclaim
		// via $__fern_release_Inner each cycle across 200k, no OOM, detector 0.
		{"cap-struct-churn", "struct Inner { xs: i32[], n: i32 } function mk(): i32 { var i = Inner { xs: [1, 2, 3, 4], n: 5 }; var f = function (): i32 { return i.n; }; return f(); } function main(): i32 { var k = 0; var n = 0; while (k < 200000) { n = mk(); k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 5},
		// Slice 2: mixed captures (string + scalar) — the string is released, the
		// scalar carries no rc and is skipped; value-correct + detector 0.
		{"cap-mixed", "function main(): i32 { var s: string = \"xy\" + \"zw\"; var a: i32 = 38; var f = function (): i32 { return s.len() + a; }; return f() + __rc_underflow_count(); }", 42},
		// Struct-ARRAY capture: the captured struct[] keeps its element type
		// inside the lambda body (cap_sa), so `ps[i].field` resolves (it read 0
		// before — a capture-typing gap), AND it deep-releases each element via
		// $__fern_arr_release_Inner on the closure's death. Value-correct.
		{"cap-structarr-val", "struct Inner { xs: i32[], n: i32 } function main(): i32 { var ps: Inner[] = [Inner { xs: [1, 2], n: 40 }, Inner { xs: [3, 4], n: 9 }]; var f = function (): i32 { return ps[0].n + ps[1].xs[1]; }; return f() + __rc_underflow_count(); }", 44},
		// Struct-array capture churn: 200k closures each capturing a struct[]
		// holding arrays; every element's array field reclaims each cycle (no
		// OOM), detector 0 — the deep per-element capture release.
		{"cap-structarr-churn", "struct Inner { xs: i32[], n: i32 } function mk(): i32 { var ps: Inner[] = [Inner { xs: [1, 2, 3], n: 4 }, Inner { xs: [5, 6, 7], n: 8 }]; var f = function (): i32 { return ps[0].n; }; return f(); } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			os.WriteFile(watPath, wat, 0o644)
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: exited %d want %d\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
