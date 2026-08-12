package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// coreIntRadixIRCases pin core/int's int_to_string_radix (i32 -> base-N string)
// on the self-host IR path (x86-64 + wasm). This is the to-string direction the
// parse-only audit (#3515) left on the AST path — but int_to_string_radix does
// NOT use __memcpy / usize (unlike int_to_string / __int_to_string_u64): it
// builds its result with __alloc_u8 + .with + string_from_bytes_unchecked, the same
// IR-eligible builder std/hex / std/base64 use. So it lowers through IR, and
// these cases prove it (routing-pinned to "ir", oracle-checked against the
// interpreter). A round-trip case threads the result back through
// parse_int_radix (already IR-audited) to confirm the bytes are correct.
//
// __radix_digit / __radix_char are core/int helpers, inlined (renamed) since the
// single-program driver resolves no imports. Each program returns a small
// deterministic int (<= 120 for the wasm exit-code clamp, #2908). No compiler
// change. FEATURE-AUDIT core/int row.
const coreIntRadixIRPrelude = `function radix_digit(c: i32): i32 {
    if (c >= 48 && c <= 57)  { return c - 48; }
    if (c >= 97 && c <= 122) { return c - 87; }
    if (c >= 65 && c <= 90)  { return c - 55; }
    return 0 - 1;
}
function radix_char(d: i32): i32 {
    if (d < 10) { return 48 + d; }
    return 97 + (d - 10);
}
function int_to_string_radix(n: i32, base: i32): string {
    if (base < 2 || base > 36) { return ""; }
    if (n == 0) { return "0"; }
    var neg: boolean = (n < 0);
    var mag: i64 = n as i64;
    if (neg) { mag = 0 - mag; }
    var digits: u8[] = __alloc_u8(33);
    var k: i32 = 0;
    var b64: i64 = base as i64;
    while (mag > (0 as i64)) {
        var d: i32 = (mag % b64) as i32;
        digits = digits.with(k, radix_char(d) as u8);
        k = k + 1;
        mag = mag / b64;
    }
    var out_len: i32 = k;
    if (neg) { out_len = k + 1; }
    var buf: u8[] = __alloc_u8(out_len);
    var bi: i32 = 0;
    if (neg) { buf = buf.with(0, 45 as u8); bi = 1; }
    var j: i32 = k - 1;
    while (j >= 0) { buf = buf.with(bi, digits[j]); bi = bi + 1; j = j - 1; }
    return string_from_bytes_unchecked(buf);
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
        var d: i32 = radix_digit(s[i] as i32);
        if (d < 0 || d >= base) { return None; }
        v = v * base + d;
        i = i + 1;
    }
    if (neg) { v = 0 - v; }
    return Some(v);
}
`

var coreIntRadixIRCases = []struct {
	name string
	main string
	want int
}{
	// hex: 255 -> "ff" (len 2).
	{"hex-len", `var s: string = int_to_string_radix(255, 16); return s.len();`, 2},
	// hex first byte: 'f' = 102.
	{"hex-byte", `var s: string = int_to_string_radix(255, 16); return s[0] as i32;`, 102},
	// binary: 10 -> "1010" (len 4).
	{"binary-len", `var s: string = int_to_string_radix(10, 2); return s.len();`, 4},
	// base36: 30 -> "u" = 117.
	{"base36-byte", `var s: string = int_to_string_radix(30, 36); return s[0] as i32;`, 117},
	// zero is the early-return "0" (len 1).
	{"zero", `var s: string = int_to_string_radix(0, 16); return s.len();`, 1},
	// negative: -255 base 16 -> "-ff" (len 3), first byte '-' = 45.
	{"neg-len", `var s: string = int_to_string_radix(0 - 255, 16); return s.len();`, 3},
	{"neg-sign", `var s: string = int_to_string_radix(0 - 255, 16); return s[0] as i32;`, 45},
	// out-of-range base -> "".
	{"bad-base", `var s: string = int_to_string_radix(10, 99); return s.len();`, 0},
	// round-trip through the (IR-audited) parser: parse(to_string(100,16),16) == 100.
	{"round-trip", `var s: string = int_to_string_radix(100, 16); match (parse_int_radix(s, 16)) { Some(v) => { return v; }, None => { return 0; } } return 0;`, 100},
}

func coreIntRadixIRSrc(mainBody string) string {
	return coreIntRadixIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostCoreIntRadixIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostCoreIntRadixIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range coreIntRadixIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(coreIntRadixIRSrc(tc.main))
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

// TestSelfHostCoreIntRadixIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostCoreIntRadixIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host core/int radix wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
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

	for _, tc := range coreIntRadixIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(coreIntRadixIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "core_int_radix_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("core/int radix wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
