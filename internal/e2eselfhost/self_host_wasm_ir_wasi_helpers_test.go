package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWasmIRWasiHelpers pins two #4801 fixes on the path that carried
// them, both reached through wasm_run.fern -> wasm.emit_module ->
// emit_module_mode's IR branch:
//
//  1. The WASI runtime-helper emission. emit_module_mode's IR branch and the
//     wasm_ir_run.fern driver used to inline two hand-maintained copies of the
//     same module emission, and the emit_module_mode copy never grew the WASI
//     env / args / clock / write_file surface the driver copy had. A module
//     using any of them emitted `call $__fern_env` (etc.) with no matching
//     definition or import, so wasmtime rejected it outright:
//     `unknown func: failed to find name $__fern_env`. Both callers now share
//     wasm.emit_ir_module, so the two cannot drift apart again.
//
//  2. i64 / f64 `const` width inference. A bare ident naming a `const` lowers
//     to a call to its zero-arg accessor, but infer_expr_width / expr_is_f64
//     only consulted local slots for ExprIdent — so `const B: i64` read as
//     `B + 1` inferred width 32 and emitted `i32.add` over a value the
//     accessor returns as i64: `type mismatch: expected i32, found i64`.
//
// Every case asserts the program's STDOUT, not just its exit code: each of these
// programs failed to link outright before the fix, so an exit-code-only check
// would also pass on a module that linked but computed the wrong value. Cases
// whose program the reference interpreter can also run are additionally
// oracle-checked against it (`oracle: true`). The env / args / file / clock
// cases cannot be: they read a wasmtime-supplied environment or wall clock the
// interpreter does not share. Nor can const-i64, whose print_int(i64) the
// native signature still rejects (#5477) — const-i64-oracled covers the same
// const through an i32 result so the oracle applies.
//
// This test is deliberately NOT quarantined: TestSelfHostWasmRun (whose subtests
// originally surfaced these) stays out of the CI shards until its remaining
// closure/f64-coercion failures are fixed, so without this the fixes would ship
// with no CI coverage at all.
func TestSelfHostWasmIRWasiHelpers(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm IR WASI-helper e2e")
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
		name   string
		src    string
		stdout string
		// oracle additionally pins the exit code against the reference
		// interpreter, for the cases it can run.
		oracle bool
	}{
		// --- helper-emission cases: each of these emitted a call to a helper
		// that was never defined, so the module failed to link at all. ---
		{
			name:   "env-some",
			src:    `function main(): i32 { match (env("FERNTEST")) { Some(v) => { write(v); return 0; }, None => { write("none"); return 0; } } return 1; }`,
			stdout: "hello123",
		},
		{
			name:   "env-none",
			src:    `function main(): i32 { match (env("FERN_NOT_SET")) { Some(v) => { write(v); return 0; }, None => { write("none"); return 0; } } return 1; }`,
			stdout: "none",
		},
		{
			name:   "args-index",
			src:    `function main(): i32 { var a = args(); write(a[1]); return 0; }`,
			stdout: "ALPHA",
		},
		{
			name:   "write-file-roundtrip",
			src:    `function main(): i32 { match (write_file("rt.txt", "roundtrip!")) { Some(e) => { return 1; }, None => { } } match (read_file("rt.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 2; } } return 3; }`,
			stdout: "roundtrip!",
		},
		{
			name:   "clock-monotonic-non-decreasing",
			src:    `function main(): i32 { var a: i64 = monotonic_ns(); var b: i64 = monotonic_ns(); if (b >= a) { print_int(1); } else { print_int(0); } return 0; }`,
			stdout: "1",
		},
		{
			name:   "clock-now-ns-positive",
			src:    `function main(): i32 { var t: i64 = now_ns(); if (t > 0) { print_int(1); } else { print_int(0); } return 0; }`,
			stdout: "1",
		},
		// --- const width inference ---
		{
			// The 64-bit OUTPUT path: print_int(i64) widens to
			// $__fern_print_int64. Not oracle-checked — print_int's native
			// signature is i32-only (#5477), so the interpreter rejects this
			// program even though the const itself now type-checks.
			name:   "const-i64",
			src:    `const BIG: i64 = 5000000000; function main(): i32 { print_int(BIG + 1); return 0; }`,
			stdout: "5000000001",
		},
		{
			// The same const through an i32-typed RESULT rather than stdout, so
			// the reference interpreter can run it: 5000000000 % 97 == 73 only
			// if the const kept its 64 bits. Oracled cases return their value
			// instead of printing it because print_int is a self-host-only
			// builtin the native checker does not accept (#5477).
			name:   "const-i64-oracled",
			src:    `const BIG: i64 = 5000000000; function main(): i32 { return (BIG % 97) as i32; }`,
			oracle: true,
		},
		{
			name:   "const-f64",
			src:    `const HALF: f64 = 3.5; function main(): i32 { return (HALF * 2.0) as i32; }`,
			oracle: true,
		},
		{
			name:   "const-i32-unchanged",
			src:    `const N: i32 = 41; function main(): i32 { return N + 1; }`,
			oracle: true,
		},
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

			rcmd := exec.Command("wasmtime", "run",
				"--env", "FERNTEST=hello123", "--dir", dir, watFile, "ALPHA", "BETA")
			out, _ := rcmd.Output()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally (likely an unlinkable module — the bug):\n%s", wat)
			}
			want := 0
			if tc.oracle {
				want = interpExit(t, interpBin, tc.src)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s: wasm exited %d, want %d", tc.name, got, want)
			}
			if tc.stdout != "" && string(out) != tc.stdout {
				t.Errorf("%s: wasm stdout = %q, want %q", tc.name, string(out), tc.stdout)
			}
		})
	}
}
