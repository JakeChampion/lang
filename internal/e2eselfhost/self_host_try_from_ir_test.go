package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tryFromIRCases pin the FROM-CONVERTING `?` (#2697) on the self-host IR path
// (x86-64 + wasm). When a `Result[_, E1]` is propagated with `?` through a
// function returning `Result[_, E2]` and E1 != E2, the failure path can't
// forward the source `Err(E1)` box unchanged — it carries an E1 payload where
// the caller expects E2. irlower's lower_try recovers the enclosing return's
// error type (LowerState.cur_ret), and when a leaf-safe struct E2 with an
// associated `from` exists, rewrites the failure path to `Err(E2.from(e))`
// (mirroring the native checker's tryConvertErrViaFrom desugar).
//
// The `err-converts*` cases are the load-bearing ones: `read(0)` yields
// `Err(IoErr{42})`; the convert wraps it to `AppErr{42+50}` = 92, so the
// handler reads e.code == 92. The pre-fix forward-the-box behaviour would
// surface the un-converted IoErr{42} (e.code == 42) — so 92-vs-42 is a direct
// witness that the conversion fired. Oracles stay <= 120 (the wasm exit-code
// clamp, #2908). Each case is routing-pinned to "ir" (asm_pathprobe_run).
const tryFromIRPrelude = `trait FromIo { function from(e: IoErr): Self; }
struct IoErr { code: i32 }
struct AppErr { code: i32 }
impl FromIo for AppErr { function from(e: IoErr): AppErr { return AppErr { code: e.code + 50 }; } }
function read(ok: i32): Result[i32, IoErr] {
    if (ok == 0) { return Err(IoErr { code: 42 }); }
    return Ok(8);
}
`

var tryFromIRCases = []struct {
	name string
	main string
	want int
}{
	// Ok path with DIFFERING error types: read(1)? unwraps Ok(8) (no
	// conversion on the success path), +1 = 9.
	{"ok-prop", `function run(ok: i32): Result[i32, AppErr] { var v = read(ok)?; return Ok(v + 1); } function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 9},
	// Error path — THE conversion witness: read(0)? propagates Err(IoErr{42}),
	// converted to Err(AppErr{92}); the handler reads e.code == 92 (NOT 42).
	{"err-converts", `function run(ok: i32): Result[i32, AppErr] { var v = read(ok)?; return Ok(v + 1); } function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 92},
	// Convert + arithmetic on the converted error field: 92 + 3 = 95 (pins that
	// the bound `e` is the converted AppErr, not the source IoErr).
	{"err-converts-add", `function run(ok: i32): Result[i32, AppErr] { var v = read(ok)?; return Ok(v + 1); } function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(e) => { return e.code + 3; } } }`, 95},
	// Two `?` in a row, both Ok across the error-type boundary: 8 + 8 = 16.
	{"two-ok", `function run(ok: i32): Result[i32, AppErr] { var a = read(ok)?; var b = read(ok)?; return Ok(a + b); } function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 16},
	// The SECOND `?` short-circuits and converts: read(1)?=8 then read(0)?
	// converts Err(IoErr{42}) -> Err(AppErr{92}); handler reads e.code == 92.
	{"second-errs", `function run(): Result[i32, AppErr] { var a = read(1)?; var b = read(0)?; return Ok(a + b); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 92},
}

func tryFromIRSrc(mainBody string) string {
	return tryFromIRPrelude + "\n" + mainBody + "\n"
}

// TestSelfHostTryFromIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, with the routing pinned to the "ir" path.
func TestSelfHostTryFromIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range tryFromIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tryFromIRSrc(tc.main))
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

// TestSelfHostTryFromIRWasm runs the same cases through the wasm IR backend, so
// the conversion is verified on the stack-machine backend too (4-byte struct
// pointers), not just the register ABI.
func TestSelfHostTryFromIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host try-from wasm IR e2e")
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

	for _, tc := range tryFromIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tryFromIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "try_from_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("try-from wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
