package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// optArithNarrowF64IRCases pin an Option/Result match-EXPRESSION arm that does
// f64 ARITHMETIC over a bound f64 payload and then narrows it with `as i32`
// (`match (o) { Some(v) => (v * 4.0) as i32, None => 0 }`) to the self-host IR
// path on x86-64 + wasm. This is the f64 sibling of the i64 arith-narrow admit
// (#3585): the arm computes a wide f64 intermediate over the payload and casts
// the whole thing to i32, so the result temp is i32 (the cast narrows) exactly
// like the i64 case. iife_arm_returns_narrowed_payload_arith was gated `ft ==
// "i64"`; #2691 extends it (and its call site in iife_payload_field_bindable) to
// f64, passing kind = ft so the f64 arith leaf/op classifier
// (iife_payload_arith_kind / _leaf_kind, which already handle f64) fires. Each
// case is oracle-checked against the interpreter and returns <= 126.
var optArithNarrowF64IRCases = []struct {
	name string
	main string
}{
	// (payload * 4.0) as i32. 2.5 * 4.0 = 10.
	{"mul-narrow", `function main(): i32 { var o: Option[f64] = Some(2.5); return match (o) { Some(v) => (v * 4.0) as i32, None => 0 }; }`},
	// (payload + 1.5) as i32. 2.5 + 1.5 = 4.
	{"add-narrow", `function main(): i32 { var o: Option[f64] = Some(2.5); return match (o) { Some(v) => (v + 1.5) as i32, None => 0 }; }`},
	// Result[f64, i32], Ok arm arith-then-narrow. 3.5 + 1.0 = 4.
	{"result-add", `function main(): i32 { var r: Result[f64, i32] = Ok(3.5); return match (r) { Ok(v) => (v + 1.0) as i32, Err(e) => e }; }`},
	// None arm taken — the arith arm is not evaluated. 7.
	{"none-taken", `function main(): i32 { var o: Option[f64] = None; return match (o) { Some(v) => (v * 4.0) as i32, None => 7 }; }`},
	// A compound f64 arith composition over the payload. 3.0 * 2.0 + 1.0 = 7.
	{"compound", `function main(): i32 { var o: Option[f64] = Some(3.0); return match (o) { Some(v) => (v * 2.0 + 1.0) as i32, None => 0 }; }`},
	// Regression: the i64 arith-narrow (already on the IR path) still works. 42.
	{"i64-keep", `function main(): i32 { var o: Option[i64] = Some(40); return match (o) { Some(v) => (v + 2) as i32, None => 0 }; }`},
}

// TestSelfHostOptArithNarrowF64IRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostOptArithNarrowF64IRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range optArithNarrowF64IRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostOptArithNarrowF64IRWasm runs the same cases through the wasm IR backend.
func TestSelfHostOptArithNarrowF64IRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host opt-arith-narrow-f64 wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range optArithNarrowF64IRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "opt_arith_narrow_f64_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("opt-arith-narrow-f64 wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
