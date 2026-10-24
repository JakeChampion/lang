package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type stdlibConcatCase struct {
	name string
	main string
}

var cryptoIRCases = []stdlibConcatCase{
	{"sha256-len", `return sha256_hex("hello".as_bytes()).len();`},
	{"sha256-abc-d0", `return sha256_hex("abc".as_bytes())[0] as i32;`},
	{"sha256-empty-d0", `return sha256_hex("".as_bytes())[0] as i32;`},
	{"sha256-bytes-len", `return sha256_bytes("abc".as_bytes()).len();`},
	{"hmac-len", `return hmac_sha256_hex([107 as u8, 101 as u8, 121 as u8], "msg".as_bytes()).len();`},
}

var pathIRCases = []stdlibConcatCase{
	{"join-len", `return join(["usr", "local", "bin"]).len();`},
	{"ext-len", `return extension("a.b.c.txt").len();`},
	{"filename-len", `return file_name("/a/b/c.txt").len();`},
	{"parent-len", `return parent("/a/b/c.txt").len();`},
}

var mathIRCases = []stdlibConcatCase{
	{"range-sum", `let a = range(0, 10); let s = 0; for x in a { s = s + x; } return s - 35;`},
	{"range-step-len", `return range_step(0, 20, 5).len();`},
	{"pack-rgb-low", `return pack_rgb(255, 128, 64) & 255;`},
	{"i32-max", `return i32_max() - 2147483527;`},
}

func stdlibConcatSource(t *testing.T, mod, mainBody string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("../../stdlib/std", mod+".fern"))
	if err != nil {
		t.Fatalf("read std/%s.fern: %v", mod, err)
	}
	out := append([]byte{}, src...)
	out = append(out, []byte("\nfunction main(): i32 { "+mainBody+" }\n")...)
	return out
}

func runStdlibConcatX86(t *testing.T, mod string, cases []stdlibConcatCase) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

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

func runStdlibConcatWasm(t *testing.T, mod string, cases []stdlibConcatCase) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host stdlib wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := stdlibConcatSource(t, mod, tc.main)
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
