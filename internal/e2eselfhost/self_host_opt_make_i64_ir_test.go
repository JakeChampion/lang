package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// optMakeI64IRCases pin the i64/u64 Option/Result CONSTRUCTION-width fix to the
// self-host IR path on x86-64 + wasm. `return Ok(40)` / `Some(40)` / `Err(40)` in
// a function whose declared payload is i64/u64 must build the 8-byte box (payload
// at offset 8) that every consumer reads via op_opt_payload_w(64, true) — match
// arms and the try-operator. Before the fix the construction inferred the payload
// width from the ARGUMENT (a bare i32 literal → 4-byte payload at offset 4), so a
// width-64 read returned garbage (the WASM backend read 0; native interp was
// correct). The fix recovers the expected payload type from the enclosing
// function's Option/Result return type and routes a bare literal / i32 arg through
// the 8-byte construction (lower_i64 / op_int_extend). Each arm READS the unwrapped
// payload value through to the exit code, so the 8-byte round-trip is exercised;
// every result is <= 126 (wasmtime exit-code truncation, cf. #2908). Oracle-checked
// against the interpreter. Mirrors self_host_nested_array_ir_test.go.
//
// NOTE: the fix is scoped to RETURN position (the expected payload type is the
// current function's return type). An annotated let-binding (`var r:
// Result[i64,_] = Ok(40)`) and the match-arm READ width for an Option[i64] *local*
// are a separate, entangled inconsistency tracked as a follow-up.
var optMakeI64IRCases = []struct {
	name string
	main string
}{
	// Ok(<i32 literal>) in a Result[i64,_] fn: payload must be 8-byte. 40/8 = 5.
	{"ok-i64-literal", `function g(): Result[i64, i32] { return Ok(40); } function main(): i32 { match (g()) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},
	// Some(<i32 literal>) in an Option[i64] fn. 40/8 = 5.
	{"some-i64-literal", `function g(): Option[i64] { return Some(40); } function main(): i32 { match (g()) { Some(v) => { return (v / 8) as i32; }, None => { return 0; } } }`},
	// Err(<i32 literal>) with an i64 error payload (Result[i32, i64]). 40/8 = 5.
	{"err-i64-literal", `function g(): Result[i32, i64] { return Err(40); } function main(): i32 { match (g()) { Ok(v) => { return v; }, Err(e) => { return (e / 8) as i32; } } }`},
	// Ok(<i32 variable>) in a Result[i64,_] fn: the i32 value is widened to 8 bytes
	// (op_int_extend), not stored as a 4-byte payload. 40/8 = 5.
	{"ok-i64-i32var", `function g(n: i32): Result[i64, i32] { return Ok(n); } function main(): i32 { match (g(40)) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},
	// u64 payload — the unsigned 8-byte construction. 99/9 = 11.
	{"ok-u64-literal", `function g(): Result[u64, i32] { return Ok(99); } function main(): i32 { match (g()) { Ok(v) => { return (v / 9) as i32; }, Err(e) => { return e; } } }`},
	// The unwrapped i64 payload flows through the try-operator (offset-8 read) and is
	// re-wrapped (offset-8 construction) before the final match reads it. 40/8 = 5.
	{"try-roundtrip-i64", `function g(): Result[i64, i32] { return Ok(40); } function f(): Result[i64, i32] { var x: i64 = g()?; return Ok(x); } function main(): i32 { match (f()) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},

	// Annotated let-binding construction: `var r: Result[i64,_] = Ok(40)` /
	// `var o: Option[i64] = Some(40)` with a bare i32 literal must build the 8-byte
	// box AND record the annotation as the slot's opt_type so the later `match` reads
	// offset-8 — both sides must agree or the read truncates. (let-ok-i64-literal was
	// a silent miscompile: wasm read 0. let-some-i64-literal was only accidentally
	// correct — i32 construct + i32 read both at offset 4 — and truncated large i64s.)
	{"let-ok-i64-literal", `function main(): i32 { var r: Result[i64, i32] = Ok(40); match (r) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},
	{"let-some-i64-literal", `function main(): i32 { var o: Option[i64] = Some(40); match (o) { Some(v) => { return (v / 8) as i32; }, None => { return 0; } } }`},
	{"let-err-i64-literal", `function main(): i32 { var r: Result[i32, i64] = Err(40); match (r) { Ok(v) => { return v; }, Err(e) => { return (e / 8) as i32; } } }`},
	// i32-variable arg in an annotated let (Result only — the Option[i64] = Some(i32var)
	// form is a checker type error). The i32 value is widened to 8 bytes (op_int_extend).
	{"let-ok-i64-i32var", `function g(n: i32): i32 { var r: Result[i64, i32] = Ok(n); match (r) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } } function main(): i32 { return g(40); }`},
	{"let-some-u64-literal", `function main(): i32 { var o: Option[u64] = Some(99); match (o) { Some(v) => { return (v / 9) as i32; }, None => { return 0; } } }`},
	// LARGE i64 values: a genuine 8-byte round-trip, NOT the accidental i32
	// cancellation (5000000000 truncated to i32 would not divide to 5). Proves the
	// match-read uses offset-8 for both Option and Result locals. 5e9 / 1e9 = 5.
	{"let-some-i64-large", `function main(): i32 { var o: Option[i64] = Some(5000000000); match (o) { Some(v) => { return (v / 1000000000) as i32; }, None => { return 0; } } }`},
	{"let-ok-i64-large", `function main(): i32 { var r: Result[i64, i32] = Ok(5000000000); match (r) { Ok(v) => { return (v / 1000000000) as i32; }, Err(e) => { return e; } } }`},
}

// TestSelfHostOptMakeI64IRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostOptMakeI64IRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range optMakeI64IRCases {
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

// TestSelfHostOptMakeI64IRWasm runs the same cases through the wasm IR backend.
func TestSelfHostOptMakeI64IRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host opt-make-i64 wasm IR e2e")
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

	for _, tc := range optMakeI64IRCases {
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
			watFile := filepath.Join(dir, "opt_make_i64_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("opt-make-i64 wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
