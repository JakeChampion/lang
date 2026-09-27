package e2eselfhost

import "testing"

// formatStringIRCases exercise std/format's `format(fmt, args)` — `{}`-
// placeholder substitution — through the self-host IR path on x86-64 + wasm.
// (TestSelfHostFormatBytesIR already covers format_bytes; `format` itself was a
// "self-host pending" audit gap.) The body is inlined as `fmt_format`; this
// verifies the constructs `format` compiles to lower on the IR path: string
// `.len()`, byte index `s[i]`, single-char `slice_unchecked`, string concat,
// and `string[]` index/`.len()` across a while loop. Each program returns the
// rendered string's length (kept <= 126) and is oracle-checked against the
// reference interpreter (cf. the hardcoded-expectation gap in #2908).
// FEATURE-AUDIT std/format row.
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
            out = out + slice_unchecked(fmt, i, i + 1);
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

// TestSelfHostFormatStringIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostFormatStringIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range formatStringIRCases {
		src := formatStringIRSrc(tc.main)
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
