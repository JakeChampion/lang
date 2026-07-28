package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hexIRCases compile the REAL std/hex module (concatenated with a main, the same
// single-module trick the std/json self-host test uses) through the self-host IR
// path on x86-64 + wasm, confirming std/hex (`hex_encode` / `hex_decode`) lowers
// end-to-end. std/hex builds on `__alloc_u8` + `.with` + bit ops + `string_from_bytes_unchecked`
// — the last of which only started lowering on the wasm IR path with the recent
// helper-gate fix; this is the module-level confirmation.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a non-negative value <= 126 (cf. #2908).
var hexIRCases = []struct {
	name string
	main string
}{
	// The module is compiled alone (no modload), so std/string's `s.bytes()`
	// isn't in scope — the u8[] encoder inputs are written as byte literals.
	{"encode-len", `return hex_encode([104 as u8, 105 as u8]).len();`},                 // "6869" -> 4
	{"encode-digit", `return hex_encode([65 as u8])[0] as i32;`},                       // 0x41 -> '4' = 52
	{"decode-len", `return hex_decode("6869").len();`},                                 // -> "hi" len 2
	{"decode-char", `return hex_decode("7a")[0] as i32;`},                              // -> 'z' = 122
	{"roundtrip", `return hex_decode(hex_encode([72 as u8, 105 as u8]))[0] as i32;`},   // 'H' = 72
	{"roundtrip-len", `return hex_decode(hex_encode([104 as u8, 101 as u8, 108 as u8, 108 as u8, 111 as u8])).len();`},           // 5
}

// hexSource reads the real std/hex.fern and appends a main (single-module, no
// modload — std/hex has no imports).
func hexSource(t *testing.T, mainBody string) []byte {
	t.Helper()
	src, err := os.ReadFile("../../internal/stdlib/std/hex.fern")
	if err != nil {
		t.Fatalf("read std/hex.fern: %v", err)
	}
	out := append([]byte{}, src...)
	out = append(out, []byte("\nfunction main(): i32 { "+mainBody+" }\n")...)
	return out
}

// TestSelfHostHexIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to the "ir" path.
func TestSelfHostHexIRX86_64(t *testing.T) {
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

	for _, tc := range hexIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := hexSource(t, tc.main)
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

// TestSelfHostHexIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostHexIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host hex wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range hexIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := hexSource(t, tc.main)
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
			watFile := filepath.Join(dir, "hex_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("hex wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
