package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// resultStructErrIRCases pin `?` error-propagation over a `Result[T, E]` whose
// error type E is a CONCRETE STRUCT (the Rust-style `Result[T, MyError]` shape),
// with field access on the bound error in the `Err` arm, on the self-host IR path
// (x86-64 + wasm). The existing `?` pins (self_host_try_op_*) use a `string`
// error; the error-trait pin (self_host_error_trait_ir) uses a `dyn Error` trait
// object — neither covers a concrete struct error. This exercises: `?` desugar
// over a struct-payload `Err`, the `Err(struct)` construction + propagation
// across the call boundary, the `Ok`/`Err` payload `match`, and struct field
// reads (`e.code`, `e.detail`) on the bound error. All already lowers, so no
// compiler change — an observability pin against a regression to the AST
// fallback.
//
// Each case is routing-pinned to "ir" (asm_pathprobe_run) and oracle-checked
// against the interpreter; results stay <= 120 (the wasm exit-code clamp, #2908).
const resultStructErrIRPrelude = `struct Ferr { code: i32, detail: i32 }
function step(ok: i32): Result[i32, Ferr] {
    if (ok == 0) { return Err(Ferr { code: 9, detail: 2 }); }
    return Ok(5);
}
`

var resultStructErrIRCases = []struct {
	name string
	main string
	want int
}{
	// happy path: step(1)? unwraps Ok(5), +1 = 6.
	{"ok-prop", `function run(): Result[i32, Ferr] { var v = step(1)?; return Ok(v + 1); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 6},
	// error path: step(0)? propagates Err(Ferr); the handler reads e.code = 9.
	{"err-prop-code", `function run(): Result[i32, Ferr] { var v = step(0)?; return Ok(v + 1); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 9},
	// two `?` in a row, both Ok: 5 + 5 = 10.
	{"two-ok", `function run(): Result[i32, Ferr] { var a = step(1)?; var b = step(1)?; return Ok(a + b); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 10},
	// the second `?` short-circuits on Err; e.code = 9 from the propagated error.
	{"second-errs", `function run(): Result[i32, Ferr] { var a = step(1)?; var b = step(0)?; return Ok(a + b); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 9},
	// read TWO fields of the struct error: e.code + e.detail = 9 + 2 = 11.
	{"two-field-err", `function run(): Result[i32, Ferr] { var v = step(0)?; return Ok(v); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code + e.detail; } } }`, 11},
}

func resultStructErrIRSrc(mainBody string) string {
	return resultStructErrIRPrelude + "\n" + mainBody + "\n"
}

// TestSelfHostResultStructErrIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostResultStructErrIRX86_64(t *testing.T) {
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

	for _, tc := range resultStructErrIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(resultStructErrIRSrc(tc.main))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostResultStructErrIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostResultStructErrIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host result-struct-err wasm IR e2e")
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

	for _, tc := range resultStructErrIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(resultStructErrIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "result_struct_err_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("result-struct-err wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
