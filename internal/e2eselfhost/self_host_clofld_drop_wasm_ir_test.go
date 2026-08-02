package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostClofldDropWasmIR is the wasm port of
// TestSelfHostClofldDropIRX86_64: the clofld routing lives in shared
// irlower.fern, and emit_wasm_struct_drop_body's fn-field arm (the wasm
// k_clo sibling) / emit_wasm_field_reclaim_body's fn gate (fr_clo) do the
// env-box release — a `__mkclo$` box is one rc-headered block, so the
// rc-guarded $__fern_arr_dec IS the shallow closure free (captures leak,
// the k_struct one-level model). Cases mirror the x86 leg: an admitted
// straight-line fn-field struct deep-drops at exit (the WAT carries the
// $__struct_drop_H call); the loop-nested capture churn reclaims each
// prior iteration's env box through $__field_reclaim_H's fn arm and stays
// BALANCED at scale; a bare closure IDENT field value and a BASE COPY
// keep the sound leak (no admission, aliases stay callable).
func TestSelfHostClofldDropWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping clofld drop wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
		// substring the emitted WAT must contain ("" skips the check).
		wantWat string
	}{
		{"clofld-drop-fires",
			`struct H { f: (i32) => i32, id: i32 } function main(): i32 { var h: H = H { f: function (x: i32): i32 { return x + 3; }, id: 1 }; var r: i32 = h.f(10); return r; }`,
			13, "call $__struct_drop_H"},
		{"clofld-capture-churn-balanced",
			`struct H { f: (i32) => i32, id: i32 } function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var k: i32 = i % 5; var h: H = H { f: function (x: i32): i32 { return x + k; }, id: i }; if (h.f(10) != 10 + k) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { var v: i32 = churn(200000); if (__rc_underflow() != 0) { return 99; } return v; }`,
			0, "call $__field_reclaim_H"},
		{"clofld-ident-excluded",
			`struct H { f: (i32) => i32, id: i32 } function main(): i32 { var g = function (x: i32): i32 { return x * 2; }; var h: H = H { f: g, id: 3 }; var r: i32 = h.f(5) + g(2) + h.id; if (r != 17) { return 90; } if (__rc_underflow() != 0) { return 99; } return 0; }`,
			0, ""},
		{"clofld-base-copy-excluded",
			`struct H { f: (i32) => i32, id: i32 } function main(): i32 { var h1: H = H { f: function (x: i32): i32 { return x + 1; }, id: 3 }; var h2: H = H { ...h1, id: 4 }; var r: i32 = h1.f(5) + h2.f(10) + h2.id; if (r != 21) { return 90; } if (__rc_underflow() != 0) { return 99; } return 0; }`,
			0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			if tc.wantWat != "" && !strings.Contains(string(wat), tc.wantWat) {
				t.Fatalf("%s: emitted WAT missing %q — the fn-field routing / arm did not reach wasm", tc.name, tc.wantWat)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (99 = over-release/underflow, 90 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
