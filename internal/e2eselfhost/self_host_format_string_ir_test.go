package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// formatStringIRCases exercise std/format's `format(fmt, args)` — `{}`-
// placeholder substitution — through the self-host IR path on x86-64 + wasm.
// (TestSelfHostFormatBytesIR already covers format_bytes; `format` itself was a
// "self-host pending" audit gap.) The single-program driver resolves no
// imports, so the body is inlined as `fmt_format`; this verifies the constructs
// `format` compiles to lower on the IR path: string `.len()`, byte index
// `s[i]`, single-char slice `s[i:i+1]`, string concat, and `string[]`
// index/`.len()` across a while loop. Each program returns the rendered
// string's length (kept <= 126) and is oracle-checked against the reference
// interpreter (cf. the hardcoded-expectation gap in #2908). FEATURE-AUDIT
// std/format row.
const formatStringIRPrelude = `function fmt_format(fmt: string, args: string[]): string {
    var n: i32 = fmt.len();
    var out: string = "";
    var i: i32 = 0;
    var argi: i32 = 0;
    while (i < n) {
        if (i + 1 < n && fmt[i] == 123 && fmt[i + 1] == 123) {
            out = out + "{";
            i = i + 2;
        } else if (i + 1 < n && fmt[i] == 125 && fmt[i + 1] == 125) {
            out = out + "}";
            i = i + 2;
        } else if (i + 1 < n && fmt[i] == 123 && fmt[i + 1] == 125) {
            if (argi < args.len()) {
                out = out + args[argi];
                argi = argi + 1;
            } else {
                out = out + "{}";
            }
            i = i + 2;
        } else {
            out = out + fmt[i:i + 1];
            i = i + 1;
        }
    }
    return out;
}
`

var formatStringIRCases = []struct {
	name string
	main string
}{
	// "a{}b{}c" + ["x","yy"] -> "axbyyc" (6).
	{"two-args", `var a: string[] = ["x", "yy"]; return fmt_format("a{}b{}c", a).len();`},
	// underflow: "{}{}" + ["x"] -> "x{}" (3) — the missing arg stays literal.
	{"underflow", `var a: string[] = ["x"]; return fmt_format("{}{}", a).len();`},
	// no placeholder: "hello" + [] -> "hello" (5).
	{"no-placeholder", `var a: string[] = []; return fmt_format("hello", a).len();`},
	// trailing text after a placeholder: "{}-end" + ["ab"] -> "ab-end" (6).
	{"trailing-text", `var a: string[] = ["ab"]; return fmt_format("{}-end", a).len();`},
	// escaped braces (Python/Rust convention): "{{}}" + [] -> "{}" (2); the
	// `{{`/`}}` are NOT consumed as a placeholder.
	{"escaped-empty", `var a: string[] = []; return fmt_format("{{}}", a).len();`},
	// `{{` -> literal "{" amid text: "a{{b" + [] -> "a{b" (3).
	{"escaped-open", `var a: string[] = []; return fmt_format("a{{b", a).len();`},
	// escape + placeholder: "{{{}}}" + ["X"] -> "{X}" (3).
	{"escape-then-arg", `var a: string[] = ["X"]; return fmt_format("{{{}}}", a).len();`},
	// `}}` -> literal "}": "x}}y" + [] -> "x}y" (3).
	{"escaped-close", `var a: string[] = []; return fmt_format("x}}y", a).len();`},
}

func formatStringIRSrc(mainBody string) string {
	return formatStringIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostFormatStringIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with the routing pinned to the "ir" path.
func TestSelfHostFormatStringIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range formatStringIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(formatStringIRSrc(tc.main))
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

// TestSelfHostFormatStringIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostFormatStringIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host format-string wasm IR e2e")
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

	for _, tc := range formatStringIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(formatStringIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "formatstr_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("format-string wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
