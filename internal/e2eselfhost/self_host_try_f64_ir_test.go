package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tryF64IRCases pin the `?` (try) operator on an f64 success payload on the
// self-host IR path (follow-up to #3810's string/enum case). f64 is an 8-byte
// payload read through op_opt_payload_w(64) — the same width-64 read the i64/u64
// payload uses — with the `var x: f64 = inner?` binding typing x's slot as f64
// from its annotation. Each case is routing-pinned to "ir" and value-pinned
// against the native interpreter oracle.
var tryF64IRCases = []struct {
	name string
	src  string
	want int
}{
	// `?` on Result[f64, string]: unwrap, `x * 2.0` then cast. 3.5*2 → 7.
	{"result_f64", `function mk(n: i32): Result[f64, string] { if (n > 0) { return Ok(3.5); } return Err("no"); }
function run(n: i32): Result[i32, string] { var x: f64 = mk(n)?; return Ok((x * 2.0) as i32); }
function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 7},
	// `?` on Option[f64]: unwrap, cast. 4.5 → 4.
	{"option_f64", `function find(n: i32): Option[f64] { if (n > 0) { return Some(4.5); } return None; }
function run(n: i32): Option[i32] { var x: f64 = find(n)?; return Some(x as i32); }
function main(): i32 { match (run(1)) { Some(v) => { return v; }, None => { return 0; } } }`, 4},
	// the Err short-circuit of an f64-payload `?`: run(0) → Err → outer Err → 42.
	{"result_f64_err", `function mk(n: i32): Result[f64, string] { if (n > 0) { return Ok(3.5); } return Err("no"); }
function run(n: i32): Result[i32, string] { var x: f64 = mk(n)?; return Ok((x * 2.0) as i32); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	// the None short-circuit of an f64-payload `?`: find(0) → None → 9.
	{"option_f64_none", `function find(n: i32): Option[f64] { if (n > 0) { return Some(4.5); } return None; }
function run(n: i32): Option[i32] { var x: f64 = find(n)?; return Some(x as i32); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 9; } } }`, 9},
}

// TestSelfHostTryF64IRX86_64 routes each case through the self-host x86-64 IR
// driver (pinned to "ir") and asserts the native-oracle exit code.
func TestSelfHostTryF64IRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range tryF64IRCases {
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
			progBin := buildBin(t, gcc, dir, "try_f64_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("try-f64 %q exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostTryF64WasmIR runs the same cases through the wasm IR backend.
func TestSelfHostTryF64WasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host try-f64 wasm IR e2e")
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

	for _, tc := range tryF64IRCases {
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
			watFile := filepath.Join(dir, "try_f64_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("try-f64 wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
