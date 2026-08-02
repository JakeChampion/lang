package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tryStructIRCases pin the `?` (try) operator on a STRUCT-payload Result/Option
// on the self-host IR path (#3802). `?` was IR-eligible only for scalar payloads
// (i32/boolean/i64/u64); a struct payload bailed the whole module to the legacy
// AST path. lower_try now admits a leaf-safe struct payload — op_opt_payload
// reads the pointer-width struct box exactly as the match-arm Some/Ok binding
// does, and the `var p: P = inner?` binding types p's slot from its annotation,
// so `p.field` resolves. Each case is routing-pinned to "ir" and value-pinned
// against the native interpreter oracle (interp == native).
var tryStructIRCases = []struct {
	name string
	src  string
	want int
}{
	// `?` on Result[Struct, string]: unwrap P, read two fields. 7→P{7,14}→21.
	{"result_struct", `struct P { x: i32, y: i32 }
function mk(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n, y: n * 2 }); } return Err("neg"); }
function run(n: i32): Result[i32, string] { var p: P = mk(n)?; return Ok(p.x + p.y); }
function main(): i32 { match (run(7)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 21},
	// `?` on Option[Struct]: unwrap Node, read field + 1. 5→Node{15}→16.
	{"option_struct", `struct Node { val: i32 }
function find(n: i32): Option[Node] { if (n > 0) { return Some(Node { val: n * 3 }); } return None; }
function run(n: i32): Option[i32] { var nd: Node = find(n)?; return Some(nd.val + 1); }
function main(): i32 { match (run(5)) { Some(v) => { return v; }, None => { return 0; } } }`, 16},
	// the failure path of a struct-payload `?`: Err short-circuits, forwarding the
	// source Err box unchanged. run(0) → Err("neg") → the outer match Err arm → 99.
	{"result_struct_err", `struct P { x: i32, y: i32 }
function mk(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n, y: n * 2 }); } return Err("neg"); }
function run(n: i32): Result[i32, string] { var p: P = mk(n)?; return Ok(p.x + p.y); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	// the None path of an Option-struct `?`: find(0) → None → short-circuit → 7.
	{"option_struct_none", `struct Node { val: i32 }
function find(n: i32): Option[Node] { if (n > 0) { return Some(Node { val: n * 3 }); } return None; }
function run(n: i32): Option[i32] { var nd: Node = find(n)?; return Some(nd.val + 1); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 7; } } }`, 7},
	// two chained `?` on struct payloads in one function (both must unwrap).
	{"two_chained", `struct P { x: i32 }
function a(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n }); } return Err("a"); }
function b(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n + 100 }); } return Err("b"); }
function run(n: i32): Result[i32, string] { var p: P = a(n)?; var q: P = b(n)?; return Ok(p.x + q.x); }
function main(): i32 { match (run(5)) { Ok(v) => { return v; }, Err(_) => { return 0; } } }`, 110},
}

// TestSelfHostTryStructIRX86_64 routes each case through the self-host x86-64 IR
// driver (pinned to "ir") and asserts the native-oracle exit code.
func TestSelfHostTryStructIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
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

	for _, tc := range tryStructIRCases {
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
			progBin := buildBin(t, gcc, dir, "try_struct_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("try-struct %q exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostTryStructWasmIR runs the same cases through the wasm IR backend.
func TestSelfHostTryStructWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host try-struct wasm IR e2e")
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

	for _, tc := range tryStructIRCases {
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
			watFile := filepath.Join(dir, "try_struct_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("try-struct wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
