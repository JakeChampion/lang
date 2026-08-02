package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// base64IRCases compile the REAL std/base64 module (concatenated with a main, the
// single-module trick the std/json / std/hex self-host tests use) through the
// self-host IR path on x86-64 + wasm, confirming `base64_encode` / `base64_decode`
// lower end-to-end. Like std/hex, std/base64 builds on `__alloc_u8` + `.with` +
// bit ops + `string_from_bytes_unchecked` (unblocked on wasm by the recent helper-gate fix).
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a non-negative value <= 126 (cf. #2908).
var base64IRCases = []struct {
	name string
	main string
}{
	// The module is compiled alone (no modload), so std/string's `s.bytes()`
	// isn't in scope — the u8[] encoder inputs are written as byte literals.
	{"encode-len-pad", `return base64_encode([104 as u8, 105 as u8]).len();`},                                                // "aGk=" -> 4
	{"encode-len-exact", `return base64_encode([77 as u8, 97 as u8, 110 as u8]).len();`},                                     // "TWFu" -> 4
	{"encode-digit", `return base64_encode([77 as u8, 97 as u8, 110 as u8])[0] as i32;`},                                     // 'T' = 84
	{"decode-len", `return base64_decode("aGk=").len();`},                                                                    // "hi" -> 2
	{"roundtrip", `return base64_decode(base64_encode([72 as u8, 105 as u8]))[0] as i32;`},                                   // 'H' = 72
	{"roundtrip-len", `return base64_decode(base64_encode([104 as u8, 101 as u8, 108 as u8, 108 as u8, 111 as u8])).len();`}, // 5
}

// base64Source reads the real std/base64.fern and appends a main (single-module,
// no modload — std/base64 has no imports).
func base64Source(t *testing.T, mainBody string) []byte {
	t.Helper()
	src, err := os.ReadFile("../../internal/stdlib/std/base64.fern")
	if err != nil {
		t.Fatalf("read std/base64.fern: %v", err)
	}
	out := append([]byte{}, src...)
	out = append(out, []byte("\nfunction main(): i32 { "+mainBody+" }\n")...)
	return out
}

// TestSelfHostBase64IRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to the "ir" path.
func TestSelfHostBase64IRX86_64(t *testing.T) {
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

	for _, tc := range base64IRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := base64Source(t, tc.main)
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

// TestSelfHostBase64IRWasm runs the same cases through the wasm IR backend.
func TestSelfHostBase64IRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host base64 wasm IR e2e")
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

	for _, tc := range base64IRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := base64Source(t, tc.main)
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
			watFile := filepath.Join(dir, "base64_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("base64 wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
