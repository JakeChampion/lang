package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// coreIntParseIRCases exercise core/int's radix parse direction
// (parse_int_radix + __radix_digit) through the self-host IR path on x86-64 +
// wasm (the `core/int` row was fully unaudited). The single-program driver
// resolves no imports, so the two functions are inlined verbatim from
// `internal/stdlib/core/int.fern` (no reserved type names involved). This
// verifies the constructs the parse direction lowers to compile on the IR path:
// `Option[i32]` `Some`/`None` returns with a payload-binding `match`, string
// indexing (`s[i]`) with char-class comparisons, a multiply-accumulate `while`
// loop, sign handling, and negation. Each program returns a small deterministic
// int (<= 126), pinned to the `"ir"` path; expectations are oracle-checked
// against the native interpreter. The `to_string` direction stays on the AST
// path (it pokes raw memory via `__alloc_u8` / `__memcpy` / `usize`), mirroring
// the std/u64 `to_string` caveat. FEATURE-AUDIT core/int row (parse direction).
const coreIntParseIRPrelude = `function __radix_digit(c: i32): i32 {
    if (c >= 48 && c <= 57)  { return c - 48; }
    if (c >= 97 && c <= 122) { return c - 87; }
    if (c >= 65 && c <= 90)  { return c - 55; }
    return 0 - 1;
}
function parse_int_radix(s: string, base: i32): Option[i32] {
    if (base < 2 || base > 36) { return None; }
    var n: i32 = s.len();
    if (n == 0) { return None; }
    var neg: boolean = false;
    var i: i32 = 0;
    if (s[0] == 45) { neg = true; i = 1; }
    else if (s[0] == 43) { i = 1; }
    if (i >= n) { return None; }
    var v: i32 = 0;
    while (i < n) {
        var d: i32 = __radix_digit(s[i] as i32);
        if (d < 0 || d >= base) { return None; }
        v = v * base + d;
        i = i + 1;
    }
    if (neg) { v = 0 - v; }
    return Some(v);
}
function parse_or(s: string, base: i32, dflt: i32): i32 {
    match (parse_int_radix(s, base)) {
        Some(v) => { return v; },
        None => { return dflt; },
    }
    return dflt;
}
`

var coreIntParseIRCases = []struct {
	name string
	main string
	want int
}{
	// hex parse with a lowercase a-f digit: 0x5a = 90 (kept <=125 so wasmtime
	// doesn't normalise the exit code: codes >=126 come back as 1 under WASI).
	{"hex", `return parse_or("5a", 16, 0);`, 90}, // 0x5a = 90
	// base-10 with leading '+' sign.
	{"plus-sign", `return parse_or("+42", 10, 0);`, 42},
	// binary "1100100" = 100.
	{"binary", `return parse_or("1100100", 2, 0);`, 100},
	// base-36 "z" = 35.
	{"base36", `return parse_or("z", 36, 0);`, 35},
	// invalid digit for the base -> None -> default 7.
	{"invalid-digit", `return parse_or("12x", 10, 7);`, 7},
	// empty string -> None -> default 9.
	{"empty", `return parse_or("", 10, 9);`, 9},
	// out-of-range base -> None -> default 5.
	{"bad-base", `return parse_or("10", 99, 5);`, 5},
	// negative result, mapped back to a small positive via arithmetic: -3 + 100 = 97.
	{"negative", `return parse_or("-3", 10, 0) + 100;`, 97},
}

func coreIntParseIRSrc(mainBody string) string {
	return coreIntParseIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostCoreIntParseIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostCoreIntParseIRX86_64(t *testing.T) {
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

	for _, tc := range coreIntParseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(coreIntParseIRSrc(tc.main))
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

// TestSelfHostCoreIntParseIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostCoreIntParseIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host core/int parse wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
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

	for _, tc := range coreIntParseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(coreIntParseIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "core_int_parse_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("core/int parse wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
