package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// errorTraitIRCases exercise the stdlib `Error` trait pattern through the
// stack-IR path.  The key shapes validated here:
//
//   - A `Result[i32, dyn Error]`-returning function that propagates a concrete
//     `E: Error` via `?` (lower_try forwards the Err box unchanged; the
//     concrete pointer's layout is identical to a `dyn Error` pointer).
//   - A statement-position `match` binding `Err(e)` where `e: dyn Error`:
//     the StmtMatch handler must admit the `dyn` payload and mark the slot
//     so that `e.method()` routes through op_dyn_dispatch.
//   - A value-position (IIFE) `match` binding `Err(e)` into an i32 temp:
//     `iife_payload_bindable` must admit the `dyn` payload.
//   - Methods on `dyn Error` that return i32 (`code()`).
//   - Methods on `dyn Error` that return `string` (`message()`) via an
//     explicit `var m: string = e.message()` binding (the declared type
//     drives string-slot marking without requiring expr_is_str to recognise
//     the dyn-dispatch call).
//
// Exit codes are the oracle.
var errorTraitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Basic Error trait dispatch: a single impl, ? propagation, statement match.
	// find(false) returns Err(NotFound{n:7}), handler forwards it as dyn Error,
	// the match binds e and calls e.code() = 7.
	{"basic-error-dyn-dispatch",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(42); } return Err(NotFound { n: 7 }); } function handler(ok: boolean): Result[i32, dyn Error] { var v: i32 = find(ok)?; return Ok(v + 1); } function main(): i32 { var result: i32 = 0; match (handler(false)) { Ok(v) => { result = v; }, Err(e) => { result = e.code(); } } return result; }`,
		7},

	// Ok path: handler(true) → Ok(43); match yields v = 43.
	{"error-ok-path",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(42); } return Err(NotFound { n: 7 }); } function handler(ok: boolean): Result[i32, dyn Error] { var v: i32 = find(ok)?; return Ok(v + 1); } function main(): i32 { var result: i32 = 0; match (handler(true)) { Ok(v) => { result = v; }, Err(e) => { result = e.code(); } } return result; }`,
		43},

	// IIFE (value-position) match with a dyn Error payload: both the Ok arm
	// and the Err arm yield i32.  iife_payload_bindable must admit the dyn
	// payload.  handler(false) → Err(NotFound{n:7}) → e.code() = 7.
	{"iife-err-arm-dyn-dispatch",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(42); } return Err(NotFound { n: 7 }); } function handler(ok: boolean): Result[i32, dyn Error] { var v: i32 = find(ok)?; return Ok(v + 1); } function main(): i32 { var a: i32 = match (handler(true)) { Ok(v) => v, Err(e) => e.code() }; var b: i32 = match (handler(false)) { Ok(v) => v, Err(e) => e.code() }; return a + b; }`,
		50}, // 43 + 7

	// Two Error impls in scope; dispatch routes to the right concrete method.
	// find_nf returns NotFound{n:3}; find_pe returns PermError{code:5}.
	// handler_nf → dyn Error(NotFound), handler_pe → dyn Error(PermError).
	// e.code() for NotFound = 3, for PermError = 5; sum = 8.
	{"two-impls-dispatch",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } struct PermError { c: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } impl Error for PermError { function code(self: Self): i32 { return self.c; } } function find_nf(): Result[i32, NotFound] { return Err(NotFound { n: 3 }); } function find_pe(): Result[i32, PermError] { return Err(PermError { c: 5 }); } function run_nf(): Result[i32, dyn Error] { var v: i32 = find_nf()?; return Ok(v); } function run_pe(): Result[i32, dyn Error] { var v: i32 = find_pe()?; return Ok(v); } function main(): i32 { var a: i32 = 0; var b: i32 = 0; match (run_nf()) { Ok(v) => { a = v; }, Err(e) => { a = e.code(); } } match (run_pe()) { Ok(v) => { b = v; }, Err(e) => { b = e.code(); } } return a + b; }`,
		8},

	// Error trait with message() → string: bind via explicit `string`
	// annotation so the slot is string-typed; then return the length.
	// NotFound.message() = "not found" (9 chars).
	{"error-message-string",
		`trait Error { function message(self: Self): string; } struct NotFound { n: i32 } impl Error for NotFound { function message(self: Self): string { return "not found"; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(1); } return Err(NotFound { n: 0 }); } function handler(ok: boolean): Result[i32, dyn Error] { var v: i32 = find(ok)?; return Ok(v); } function main(): i32 { var msg_len: i32 = 0; match (handler(false)) { Ok(v) => { msg_len = 0; }, Err(e) => { var m: string = e.message(); msg_len = m.len(); } } return msg_len; }`,
		9},

	// Combined: Error trait has both code() and message(); use both in Err arm.
	// code() = 4, message = "oops" (4 chars); result = 4 + 4 = 8.
	{"error-code-and-message",
		`trait Error { function code(self: Self): i32; function message(self: Self): string; } struct Fail { c: i32 } impl Error for Fail { function code(self: Self): i32 { return self.c; } function message(self: Self): string { return "oops"; } } function run(): Result[i32, Fail] { return Err(Fail { c: 4 }); } function handler(): Result[i32, dyn Error] { var v: i32 = run()?; return Ok(v); } function main(): i32 { var result: i32 = 0; match (handler()) { Ok(v) => { result = v; }, Err(e) => { var m: string = e.message(); result = e.code() + m.len(); } } return result; }`,
		8},
}

// TestSelfHostErrorTraitIRX86_64 routes each case through the self-hosted
// x86-64 driver (asm_run) and asserts the exit code, AND probes the routing
// (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostErrorTraitIRX86_64(t *testing.T) {
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

	for _, tc := range errorTraitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostErrorTraitIRWasm runs the same cases through the wasm IR backend
// (wasm_ir_run -ir).
func TestSelfHostErrorTraitIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host error-trait wasm IR e2e")
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

	for _, tc := range errorTraitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "error_trait_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("error-trait wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
