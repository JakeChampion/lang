package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stringFromBytesIRCases exercise `string_from_bytes_unchecked(u8[])` through the self-host
// IR path on x86-64 + wasm.
//
// The wasm IR backend emitted `op_str_from_bytes` as a `call
// $__fern_string_from_bytes`, but `wasm_ir_run` had no gate to actually emit that
// helper (unlike its sibling `str_bytes`), so any IR-path program packing bytes
// into a string failed to link ("unknown func $__fern_string_from_bytes"). x86 /
// arm64 already emitted the helper. The fix adds the missing
// `module_emits_op(mod, "str_from_bytes")` gate (and exports the helper).
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a non-negative value <= 126 (cf. #2908).
const stringFromBytesPrelude = `function hex_lc(n: i32): i32 { if (n < 10) { return 48 + n; } return 97 + (n - 10); }
function hexenc(s: string): string {
    var n: i32 = s.len();
    if (n == 0) { return ""; }
    var buf: u8[] = __alloc_u8(n * 2);
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = s[i];
        buf = buf.with(i * 2, hex_lc((b >> 4) & 15) as u8);
        buf = buf.with(i * 2 + 1, hex_lc(b & 15) as u8);
        i = i + 1;
    }
    return string_from_bytes_unchecked(buf);
}
`

var stringFromBytesIRCases = []struct {
	name string
	main string
}{
	// Minimal direct use: pack [72, 105] ("Hi") -> length 2.
	{"direct-len", `function main(): i32 { var b: u8[] = __alloc_u8(2); b = b.with(0, 72 as u8); b = b.with(1, 105 as u8); return string_from_bytes_unchecked(b).len(); }`},
	// Round-trip a byte through the packed string: [65]("A")[0] = 65.
	{"direct-byte", `function main(): i32 { var b: u8[] = __alloc_u8(1); b = b.with(0, 65 as u8); return string_from_bytes_unchecked(b)[0] as i32; }`},
	// hex_encode: "A" -> "41"; first digit '4' = 52.
	{"hex-digit0", stringFromBytesPrelude + `function main(): i32 { return hexenc("A")[0] as i32; }`},
	// hex_encode: "z" (0x7a) -> "7a"; second digit 'a' = 97.
	{"hex-digit1", stringFromBytesPrelude + `function main(): i32 { return hexenc("z")[1] as i32; }`},
	// hex_encode length: "hello" -> 10 hex chars.
	{"hex-len", stringFromBytesPrelude + `function main(): i32 { return hexenc("hello").len(); }`},
}

// TestSelfHostStringFromBytesIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostStringFromBytesIRX86_64(t *testing.T) {
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

	for _, tc := range stringFromBytesIRCases {
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

// TestSelfHostStringFromBytesIRWasm runs the same cases through the wasm IR
// backend — the path the missing-helper bug affected.
func TestSelfHostStringFromBytesIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host string_from_bytes_unchecked wasm IR e2e")
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

	for _, tc := range stringFromBytesIRCases {
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
			watFile := filepath.Join(dir, "sfb_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("string_from_bytes_unchecked wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
