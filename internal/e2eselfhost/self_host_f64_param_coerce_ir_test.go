package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostF64ParamCoerceIR pins that an INTEGER argument bound to an f64
// parameter is converted rather than pushed raw.
//
// irlower's call-argument dispatch had a `param_is_i64` arm (lowering the
// argument through lower_i64 so an i32 source widens) but no f64 sibling, so
// `addhalf(3)` at `addhalf(x: f64)` pushed a bare i32 and wasm rejected the
// whole module:
//
//	Invalid input WebAssembly code ...: type mismatch: expected f64, found i32
//
// The fix registers an f64 PARAM as flag '4' in the existing "ret+params"
// signature registry that already backs param_is_i64 — no new LowerState field
// — and converts via op_i32_to_f64 at the call site. Part of #4801.
//
// Oracle-checked against the reference interpreter so a wrong-but-stable value
// cannot pass.
func TestSelfHostF64ParamCoerceIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f64-param-coerce IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_run.fern",
	} {
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
		// The bug: an integer literal at an f64 param. 3 -> 3.5 -> 3.
		{"int-literal-arg", `function addhalf(x: f64): f64 { return x + 0.5; } function main(): i32 { return addhalf(3) as i32; }`, 3},
		// A float argument at the same param must be unaffected by the new arm.
		{"float-arg-unchanged", `function addhalf(x: f64): f64 { return x + 0.5; } function main(): i32 { return addhalf(3.25) as i32; }`, 3},
		// An f64-typed local, likewise unaffected.
		{"f64-local-arg", `function addhalf(x: f64): f64 { return x + 0.5; } function main(): i32 { var v: f64 = 2.5; return addhalf(v) as i32; }`, 3},
		// Mixed signature: the coercion must apply per-parameter, not to the
		// whole call — b stays an i32. 1 + 3 + 2 == 6.
		{"mixed-params", `function m(a: f64, b: i32, c: f64): f64 { return a + c + (b as f64); } function main(): i32 { return m(1, 2, 3) as i32; }`, 6},
		// The i64 param path shares the same signature registry, so pin that
		// adding the '4' flag did not disturb it. 21*2 == 42.
		{"i64-param-unaffected", `function d(x: i64): i64 { return x * 2; } function main(): i32 { return (d(21) % 97) as i32; }`, 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally (likely type-invalid WAT — the bug):\n%s", wat)
			}
			want := interpExit(t, interpBin, tc.src)
			if want != tc.exit {
				t.Fatalf("interp oracle returned %d, but the case expects %d — fix the case", want, tc.exit)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s: wasm exited %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
