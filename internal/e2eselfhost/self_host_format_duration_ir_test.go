package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// formatDurationIRCases exercise std/format's `format_duration_ms` through the
// self-host IR path on x86-64 + wasm — the sibling of format_bytes, and the
// remaining "self-host pending" piece of std/format after format / format_bytes.
// The single-program driver resolves no imports, so `ms.abs()` (std/i32) is
// inlined as a free `i32_abs`; the duration LOGIC is what's covered: an
// if-ladder over integer div/sub/mul, `i32.to_string()` (a self-host builtin),
// string concat, and `.len()`. Each program returns the rendered string's
// length (kept <= 126) and is oracle-checked against the interpreter (cf. the
// hardcoded-expectation gap in #2908). FEATURE-AUDIT std/format row.
//
// `import "std/i32"` is present so the native interpreter oracle can resolve
// `.to_string()` (a self-host builtin, but a std/i32 method natively); the
// self-host single-program driver treats `.to_string()` as a builtin and still
// routes the program through the IR path.
const formatDurationIRPrelude = `import "std/i32";
function i32_abs(n: i32): i32 { if (n < 0) { return 0 - n; } return n; }
function fmt_duration_ms(ms: i32): string {
    if (ms == 0) { return "0ms"; }
    var neg: boolean = (ms < 0);
    var mag: i32 = ms;
    if (neg) { mag = i32_abs(ms); }
    var sign: string = "";
    if (neg) { sign = "-"; }
    var h: i32 = mag / 3600000;
    var rem: i32 = mag - h * 3600000;
    var m: i32 = rem / 60000;
    rem = rem - m * 60000;
    var s: i32 = rem / 1000;
    var msPart: i32 = rem - s * 1000;
    var out: string = "";
    if (h > 0) { out = out + h.to_string() + "h"; }
    if (m > 0) { if (out.len() > 0) { out = out + " "; } out = out + m.to_string() + "m"; }
    if (s > 0) { if (out.len() > 0) { out = out + " "; } out = out + s.to_string() + "s"; }
    if (msPart > 0) { if (out.len() > 0) { out = out + " "; } out = out + msPart.to_string() + "ms"; }
    return sign + out;
}
`

var formatDurationIRCases = []struct {
	name string
	main string
}{
	// 0 -> "0ms" (3).
	{"zero", `return fmt_duration_ms(0).len();`},
	// 500 -> "500ms" (5).
	{"sub-second", `return fmt_duration_ms(500).len();`},
	// 90000 -> "1m 30s" (6).
	{"minutes-seconds", `return fmt_duration_ms(90000).len();`},
	// 3661001 -> "1h 1m 1s 1ms" (12).
	{"all-units", `return fmt_duration_ms(3661001).len();`},
	// -1500 -> "-1s 500ms" (9): negative sign + abs path.
	{"negative", `return fmt_duration_ms(0 - 1500).len();`},
}

func formatDurationIRSrc(mainBody string) string {
	return formatDurationIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostFormatDurationIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with the routing pinned to the "ir" path.
func TestSelfHostFormatDurationIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range formatDurationIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(formatDurationIRSrc(tc.main))
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

// TestSelfHostFormatDurationIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostFormatDurationIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host format-duration wasm IR e2e")
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

	for _, tc := range formatDurationIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(formatDurationIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "formatdur_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("format-duration wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
