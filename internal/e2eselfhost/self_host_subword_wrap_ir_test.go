package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// subwordWrapIRCases guard a real correctness bug: u8 arithmetic (`+` `-` `*`
// `<<`) was NOT masked back to its width on the self-host IR path, so an
// overflowing result (e.g. `255u8 + 1`) kept its full value on every IR
// backend (256) instead of wrapping (0). The interpreter and the native Go
// backends wrap per the declared width (`signExtend` by IntWidth), so this
// was a silent miscompile — the program routed through "ir" and computed the
// wrong answer. The fix records each slot's sub-word kind (local_subword, the
// sub-32-bit sibling of local_is_u32) and emits an int_cast after
// `+`/`-`/`*`/`<<` whose result is sub-word, masking exactly as `as u8`
// already does.
//
// This originally also covered i8/u16/i16, but those types were removed from
// the language (#4408); u8 is the only sub-word type left, so the i8
// (sign-extend) and u16/i16 (wider sub-word) cases are gone rather than
// force-substituted onto a type that would test something different — u8's
// add/mul/shift/sub coverage below still exercises the same masking bug.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a value in [0,126] (an equality branch reduces the wrapped result to a
// small code, cf. the wasmtime exit-code gap #2908).
var subwordWrapIRCases = []struct {
	name string
	main string
}{
	// u8 add overflow: 255 + 1 = 0 (wrap).
	{"u8-add-wrap", `function main(): i32 { var a: u8 = 255 as u8; var b: u8 = 1 as u8; var s: u8 = a + b; if ((s as i32) == 0) { return 5; } return 9; }`},
	// u8 mul overflow: 16 * 16 = 256 -> 0.
	{"u8-mul-wrap", `function main(): i32 { var a: u8 = 16 as u8; var b: u8 = 16 as u8; var s: u8 = a * b; if ((s as i32) == 0) { return 5; } return 9; }`},
	// u8 shift overflow: 1 << 8 = 256 -> 0.
	{"u8-shl-wrap", `function main(): i32 { var a: u8 = 1 as u8; var s: u8 = a << (8 as u8); if ((s as i32) == 0) { return 5; } return 9; }`},
	// u8 no overflow: 60 + 40 = 100 stays exact (kept <126 for wasmtime, #2908).
	{"u8-add-exact", `function main(): i32 { var a: u8 = 60 as u8; var b: u8 = 40 as u8; return (a + b) as i32; }`},
	// u8 subtract underflow: 0 - 1 = 255.
	{"u8-sub-wrap", `function main(): i32 { var a: u8 = 0 as u8; var b: u8 = 1 as u8; var s: u8 = a - b; if ((s as i32) == 255) { return 5; } return 9; }`},
}

// TestSelfHostSubwordWrapIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostSubwordWrapIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range subwordWrapIRCases {
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

// TestSelfHostSubwordWrapIRWasm runs the same cases through the wasm IR backend (one
// of the backends the un-masked sub-word arithmetic affected).
func TestSelfHostSubwordWrapIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host sub-word wrap wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range subwordWrapIRCases {
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
			watFile := filepath.Join(dir, "subword_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("sub-word wrap wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
