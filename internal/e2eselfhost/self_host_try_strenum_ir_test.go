package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tryStrEnumIRCases pin the `?` (try) operator on STRING and ENUM success
// payloads on the self-host IR path (#3810, follow-up to #3802's struct case).
// Both are pointer-width payloads handled exactly like the struct case:
// op_opt_payload loads the box pointer, and the `var x: T = inner?` binding types
// x's slot from its annotation (is_str for a string, is_enum_like_name→struct_ty
// for an enum). Each case is routing-pinned to "ir" and value-pinned against the
// native interpreter oracle.
var tryStrEnumIRCases = []struct {
	name string
	src  string
	want int
}{
	// `?` on Result[string, string]: unwrap, read .len(). "yes" → 3.
	{"result_string", `function mk(n: i32): Result[string, string] { if (n > 0) { return Ok("yes"); } return Err("no"); }
function run(n: i32): Result[i32, string] { var s: string = mk(n)?; return Ok(s.len()); }
function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 3},
	// `?` on Option[string]: unwrap, read .len(). "abcd" → 4.
	{"option_string", `function find(n: i32): Option[string] { if (n > 0) { return Some("abcd"); } return None; }
function run(n: i32): Option[i32] { var s: string = find(n)?; return Some(s.len()); }
function main(): i32 { match (run(1)) { Some(v) => { return v; }, None => { return 0; } } }`, 4},
	// `?` on Result[Enum, string]: unwrap the enum, then match it. Add(8) → 8.
	{"result_enum", `enum Cmd { Add(i32), Sub(i32) }
function mk(n: i32): Result[Cmd, string] { if (n > 0) { return Ok(Add(n)); } return Err("x"); }
function ev(c: Cmd): i32 { match (c) { Add(v) => { return v; }, Sub(v) => { return 0 - v; } } }
function run(n: i32): Result[i32, string] { var c: Cmd = mk(n)?; return Ok(ev(c)); }
function main(): i32 { match (run(8)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 8},
	// the Err short-circuit of a string-payload `?`: run(0) → Err → outer Err → 42.
	{"result_string_err", `function mk(n: i32): Result[string, string] { if (n > 0) { return Ok("yes"); } return Err("no"); }
function run(n: i32): Result[i32, string] { var s: string = mk(n)?; return Ok(s.len()); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	// the None short-circuit of an enum-payload `?`: find(0) → None → 7.
	{"option_enum_none", `enum Cmd { Add(i32), Sub(i32) }
function find(n: i32): Option[Cmd] { if (n > 0) { return Some(Add(n)); } return None; }
function ev(c: Cmd): i32 { match (c) { Add(v) => { return v; }, Sub(v) => { return 0 - v; } } }
function run(n: i32): Option[i32] { var c: Cmd = find(n)?; return Some(ev(c)); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 7; } } }`, 7},
}

// TestSelfHostTryStrEnumIRX86_64 routes each case through the self-host x86-64 IR
// driver (pinned to "ir") and asserts the native-oracle exit code.
func TestSelfHostTryStrEnumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range tryStrEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "try_strenum_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("try-strenum %q exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostTryStrEnumWasmIR runs the same cases through the wasm IR backend.
func TestSelfHostTryStrEnumWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host try-strenum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range tryStrEnumIRCases {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "try_strenum_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("try-strenum wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
