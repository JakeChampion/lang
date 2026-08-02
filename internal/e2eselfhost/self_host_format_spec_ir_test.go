package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// formatSpecIRCases exercise std/format's Rust-style
// `{:[fill]align[sign][0]width.precision}` format specs (issue #2684) through the
// self-host IR path on x86-64 + wasm — including the `+` sign and sign-aware `0`
// zero-pad flags (the latter slices a leading sign off with `val[1:val.len()]`).
// (TestSelfHostFormatStringIR* already covers the bare `{}` substitution.) The
// single-program driver resolves no imports, so the whole format + spec machinery
// is inlined as `fmt_format` and helpers; this verifies the constructs the spec
// path lowers to compile on the IR path: forward `}`-scan with `s[a:b]` slices,
// byte compares on `spec[p]`, `boolean`-returning helpers with `||`, an int-coded
// align switch, and the fill-repeat concat loop. Each program returns the rendered
// string's length (kept <= 126) and is oracle-checked against the reference
// interpreter. FEATURE-AUDIT std/format row.
const formatSpecIRPrelude = `function fmt_is_align(c: i32): boolean {
    return c == 60 || c == 62 || c == 94;
}
function fmt_align_code(c: i32): i32 {
    if (c == 60) { return 1; }
    if (c == 62) { return 2; }
    return 3;
}
function fmt_repeat(s: string, count: i32): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < count) { out = out + s; i = i + 1; }
    return out;
}
function fmt_apply_spec(s: string, spec: string): string {
    if (spec.len() == 0) { return s; }
    var m: i32 = spec.len();
    var p: i32 = 1;
    var fill: str = " ";
    var align: i32 = 1;
    if (p + 1 < m && fmt_is_align(spec[p + 1] as i32)) {
        fill = spec[p:p + 1];
        align = fmt_align_code(spec[p + 1] as i32);
        p = p + 2;
    } else if (p < m && fmt_is_align(spec[p] as i32)) {
        align = fmt_align_code(spec[p] as i32);
        p = p + 1;
    }
    var plus: boolean = false;
    if (p < m && spec[p] == 43) { plus = true; p = p + 1; }
    else if (p < m && spec[p] == 45) { p = p + 1; }
    var zero: boolean = false;
    if (p < m && spec[p] == 48) { zero = true; p = p + 1; }
    var width: i32 = 0;
    while (p < m && spec[p] >= 48 && spec[p] <= 57) {
        width = width * 10 + ((spec[p] as i32) - 48);
        p = p + 1;
    }
    var val: string = s;
    if (p < m && spec[p] == 46) {
        p = p + 1;
        var prec: i32 = 0;
        while (p < m && spec[p] >= 48 && spec[p] <= 57) {
            prec = prec * 10 + ((spec[p] as i32) - 48);
            p = p + 1;
        }
        if (val.len() > prec) { val = val[0:prec] + ""; }
    }
    if (plus && val.len() > 0 && val[0] >= 48 && val[0] <= 57) {
        val = "+" + val;
    }
    var vlen: i32 = val.len();
    if (vlen >= width) { return val; }
    var pad: i32 = width - vlen;
    if (zero) {
        if (val.len() > 0 && (val[0] == 45 || val[0] == 43)) {
            return val[0:1] + fmt_repeat("0", pad) + val[1:val.len()];
        }
        return fmt_repeat("0", pad) + val;
    }
    if (align == 2) { return fmt_repeat(fill, pad) + val; }
    if (align == 3) {
        var left: i32 = pad / 2;
        return fmt_repeat(fill, left) + val + fmt_repeat(fill, pad - left);
    }
    return val + fmt_repeat(fill, pad);
}
function fmt_format(fmt: string, args: string[]): string {
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
        } else if (fmt[i] == 123) {
            var j: i32 = i + 1;
            while (j < n && fmt[j] != 125) { j = j + 1; }
            var isPlaceholder: boolean = false;
            var spec: str = "";
            if (j < n) {
                spec = fmt[i + 1:j];
                if (spec.len() == 0) { isPlaceholder = true; }
                else if (spec[0] == 58) { isPlaceholder = true; }
            }
            if (isPlaceholder) {
                if (argi < args.len()) {
                    out = out + fmt_apply_spec(args[argi], spec);
                    argi = argi + 1;
                } else {
                    out = out + "{" + spec + "}";
                }
                i = j + 1;
            } else {
                out = out + fmt[i:i + 1];
                i = i + 1;
            }
        } else {
            out = out + fmt[i:i + 1];
            i = i + 1;
        }
    }
    return out;
}
`

var formatSpecIRCases = []struct {
	name string
	main string
}{
	// right-align width 8: "[{:>8}]" + ["hi"] -> "[      hi]" (10).
	{"right", `var a: string[] = ["hi"]; return fmt_format("[{:>8}]", a).len();`},
	// left-align width 8: "[{:<8}]" + ["hi"] -> "[hi      ]" (10).
	{"left", `var a: string[] = ["hi"]; return fmt_format("[{:<8}]", a).len();`},
	// center width 7: "[{:^7}]" + ["hi"] -> "[  hi   ]" (9).
	{"center", `var a: string[] = ["hi"]; return fmt_format("[{:^7}]", a).len();`},
	// custom fill '*' right-align: "[{:*>6}]" + ["ab"] -> "[****ab]" (8).
	{"fill", `var a: string[] = ["ab"]; return fmt_format("[{:*>6}]", a).len();`},
	// precision (truncate): "[{:.3}]" + ["hello"] -> "[hel]" (5).
	{"precision", `var a: string[] = ["hello"]; return fmt_format("[{:.3}]", a).len();`},
	// precision + width: "[{:>8.3}]" + ["hello"] -> "[     hel]" (10).
	{"prec-width", `var a: string[] = ["hello"]; return fmt_format("[{:>8.3}]", a).len();`},
	// non-spec braces render literally, consuming no arg: "{x}" + [] -> "{x}" (3).
	{"literal-braces", `var a: string[] = []; return fmt_format("{x}", a).len();`},
	// underflow with a spec emits the placeholder verbatim: "{:>4}" + [] -> "{:>4}" (5).
	{"underflow-spec", `var a: string[] = []; return fmt_format("{:>4}", a).len();`},
	// plain `{}` still works: "{}" + ["x"] -> "x" (1).
	{"plain", `var a: string[] = ["x"]; return fmt_format("{}", a).len();`},
	// sign flag: "[{:+}]" + ["42"] -> "[+42]" (5).
	{"sign-plus", `var a: string[] = ["42"]; return fmt_format("[{:+}]", a).len();`},
	// sign flag is a no-op on a value that already carries '-': "[{:+}]" + ["-7"] -> "[-7]" (4).
	{"sign-neg", `var a: string[] = ["-7"]; return fmt_format("[{:+}]", a).len();`},
	// zero-pad: "[{:05}]" + ["42"] -> "[00042]" (7).
	{"zero-pad", `var a: string[] = ["42"]; return fmt_format("[{:05}]", a).len();`},
	// sign-aware zero-pad keeps '-' leading: "[{:05}]" + ["-42"] -> "[-0042]" (7).
	{"zero-pad-neg", `var a: string[] = ["-42"]; return fmt_format("[{:05}]", a).len();`},
	// sign + zero-pad combine: "[{:+06}]" + ["42"] -> "[+00042]" (8).
	{"sign-zero-pad", `var a: string[] = ["42"]; return fmt_format("[{:+06}]", a).len();`},
}

func formatSpecIRSrc(mainBody string) string {
	return formatSpecIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostFormatSpecIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with the routing pinned to the "ir" path.
func TestSelfHostFormatSpecIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range formatSpecIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(formatSpecIRSrc(tc.main))
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

// TestSelfHostFormatSpecIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostFormatSpecIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host format-spec wasm IR e2e")
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

	for _, tc := range formatSpecIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(formatSpecIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "formatspec_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("format-spec wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
