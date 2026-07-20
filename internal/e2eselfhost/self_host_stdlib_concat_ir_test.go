package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stdlibConcatCase compiles a REAL no-import std module (concatenated with a main —
// the single-module trick the std/hex / std/base64 self-host tests use; std/json
// migrated off it to the loader driver when json grew a real cross-module
// method dependency, #5420)
// through the self-host IR path on x86-64 + wasm, confirming the module's functions
// lower end-to-end. crypto/sha256 is the standout: it drives the whole u32-heavy
// SHA-256 message schedule + compression (rotr / shr_u / wrapping add) through the
// IR path. Each case is oracle-checked against the interpreter, routing-pinned to
// "ir", and returns a value <= 120 (cf. the wasmtime exit-code gap #2908).
type stdlibConcatCase struct {
	name string
	main string
}

var cryptoIRCases = []stdlibConcatCase{
	{"sha256-len", `return sha256_hex("hello").len();`},         // 64 hex chars
	{"sha256-abc-d0", `return sha256_hex("abc")[0] as i32;`},    // 'b' (0xba...) = 98
	{"sha256-empty-d0", `return sha256_hex("")[0] as i32;`},     // 'e' (0xe3...) = 101
	{"sha256-bytes-len", `return sha256_bytes("abc").len();`},   // 32 raw bytes
	{"hmac-len", `return hmac_sha256_hex("key", "msg").len();`}, // 64 hex chars
}

var pathIRCases = []stdlibConcatCase{
	{"join-len", `return path_join(["usr", "local", "bin"]).len();`}, // "usr/local/bin" = 13
	{"ext-len", `return path_extension("a.b.c.txt").len();`},         // "txt" = 3
	{"filename-len", `return path_file_name("/a/b/c.txt").len();`},   // "c.txt" = 5
	{"parent-len", `return path_parent("/a/b/c.txt").len();`},        // "/a/b" = 4
}

var mathIRCases = []stdlibConcatCase{
	{"range-sum", `var a = range(0, 10); var s = 0; for x in a { s = s + x; } return s - 35;`}, // 45-35=10
	{"range-step-len", `return range_step(0, 20, 5).len();`},                                   // [0,5,10,15] = 4
	{"pack-rgb-low", `return pack_rgb(255, 128, 64) & 255;`},                                   // 64
	{"i32-max", `return i32_max() - 2147483527;`},                                              // 120
}

func stdlibConcatSource(t *testing.T, mod, mainBody string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("../../internal/stdlib/std", mod+".fern"))
	if err != nil {
		t.Fatalf("read std/%s.fern: %v", mod, err)
	}
	out := append([]byte{}, src...)
	out = append(out, []byte("\nfunction main(): i32 { "+mainBody+" }\n")...)
	return out
}

// runStdlibConcatX86 routes each case through the self-hosted x86-64 IR driver.
func runStdlibConcatX86(t *testing.T, mod string, cases []stdlibConcatCase) {
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

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := stdlibConcatSource(t, mod, tc.main)
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

// runStdlibConcatWasm routes each case through the wasm IR backend.
func runStdlibConcatWasm(t *testing.T, mod string, cases []stdlibConcatCase) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host stdlib wasm IR e2e")
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

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := stdlibConcatSource(t, mod, tc.main)
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
			watFile := filepath.Join(dir, "stdlib_concat_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("stdlib %s wasm IR %q = %d, want %d (interp oracle)", mod, tc.name, code, want)
			}
		})
	}
}

func TestSelfHostCryptoIRX86_64(t *testing.T) { runStdlibConcatX86(t, "crypto", cryptoIRCases) }
func TestSelfHostCryptoIRWasm(t *testing.T)   { runStdlibConcatWasm(t, "crypto", cryptoIRCases) }
func TestSelfHostPathIRX86_64(t *testing.T)   { runStdlibConcatX86(t, "path", pathIRCases) }
func TestSelfHostPathIRWasm(t *testing.T)     { runStdlibConcatWasm(t, "path", pathIRCases) }
func TestSelfHostMathIRX86_64(t *testing.T)   { runStdlibConcatX86(t, "math", mathIRCases) }
func TestSelfHostMathIRWasm(t *testing.T)     { runStdlibConcatWasm(t, "math", mathIRCases) }
