package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tryTupleIRCases pin the `?` (try) operator on a TUPLE success payload on the
// self-host IR path — the last `?`-payload shape that bailed to AST (after
// #3810's string/enum and #3822's f64). A tuple is pointer-boxed, so the
// success read is the default pointer-width op_opt_payload (exactly like a
// struct); the `var t: (A, B) = inner?` binding types t's slot via
// mark_tuple_elems from its annotation so a later `t.N` resolves each element.
// Each case is routing-pinned to "ir" and value-pinned against the native
// interpreter oracle.
var tryTupleIRCases = []struct {
	name string
	src  string
	want int
}{
	// `?` on Result[(i32, string), string]: unwrap, read .0 → 7.
	{"tuple_first", `function mk(n: i32): Result[(i32, string), string] { if (n > 0) { return Ok((7, "hi")); } return Err("no"); }
function run(n: i32): Result[i32, string] { var x: (i32, string) = mk(n)?; return Ok(x.0); }
function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 7},
	// `?` on Option[(i32, i32)]: unwrap, sum the elements. (4,5) → 9.
	{"tuple_sum", `function find(n: i32): Option[(i32, i32)] { if (n > 0) { return Some((4, 5)); } return None; }
function run(n: i32): Option[i32] { var p: (i32, i32) = find(n)?; return Some(p.0 + p.1); }
function main(): i32 { match (run(1)) { Some(v) => { return v; }, None => { return 0; } } }`, 9},
	// the Err short-circuit of a tuple-payload `?`: run(0) → Err → outer Err → 42.
	{"tuple_err", `function mk(n: i32): Result[(i32, string), string] { if (n > 0) { return Ok((7, "hi")); } return Err("no"); }
function run(n: i32): Result[i32, string] { var x: (i32, string) = mk(n)?; return Ok(x.0); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	// the None short-circuit of a tuple-payload `?`: find(0) → None → 8.
	{"tuple_none", `function find(n: i32): Option[(i32, i32)] { if (n > 0) { return Some((4, 5)); } return None; }
function run(n: i32): Option[i32] { var p: (i32, i32) = find(n)?; return Some(p.0 + p.1); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 8; } } }`, 8},
}

// TestSelfHostTryTupleIRX86_64 routes each case through the self-host x86-64 IR
// driver (pinned to "ir") and asserts the native-oracle exit code.
func TestSelfHostTryTupleIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range tryTupleIRCases {
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
			progBin := buildBin(t, gcc, dir, "try_tuple_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("try-tuple %q exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostTryTupleWasmIR runs the same cases through the wasm IR backend.
func TestSelfHostTryTupleWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host try-tuple wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tryTupleIRCases {
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
			watFile := filepath.Join(dir, "try_tuple_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("try-tuple wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
