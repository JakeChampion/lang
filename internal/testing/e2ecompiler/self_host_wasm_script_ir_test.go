package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// SCRIPT-shaped modules on the wasm IR path.
//
// A script is top-level statements with no `main` — `return 42;` is the smallest.
// asmcore.synth_script_main desugars these into `function main(): i32 { … }`
// (ircore.script_normalized), and wasm_ir.route_normalized applies it for BOTH
// the emitter and the `-decide` probe, so the probe cannot report a verdict for
// a module the emitter does not judge. Each case asserts the route as well as
// the answer.
func TestSelfHostWasmScriptRoutesIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// The smallest script there is.
		{"bare-return", "return 42;\n", 42},
		// Top-level locals + a call, then a return: the shape synth_script_main
		// moves wholesale into the synthesized main.
		{"locals-and-call", "let x: i32 = 8;\nlet y: i32 = 34;\nprint_int(x + y);\nreturn x + y;\n", 42},
		// No trailing return: synth_script_main appends `return 0;`.
		{"no-trailing-return", "let x: i32 = 1;\nprint_int(x);\n", 0},
		// A script that defines functions AND has top-level statements — still
		// script-shaped, because none of them is `main`.
		{"funcs-plus-toplevel", "function double(n: i32): i32 { return n * 2; }\nlet v: i32 = double(21);\nreturn v;\n", 42},
		// Control: a module that already has `main` is NOT a script, and must be
		// untouched by the normalisation.
		{"has-main-control", "function main(): i32 { return 7; }\n", 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(withPrintInt(tc.src))
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — scripts no longer lower", tc.name, route)
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
