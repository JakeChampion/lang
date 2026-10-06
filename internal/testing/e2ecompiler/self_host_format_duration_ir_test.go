package e2ecompiler

import "testing"

// formatDurationIRCases exercise std/format's `format_duration_ms` through the
// self-host IR path on x86-64 + wasm — the sibling of format_bytes, and the
// remaining "self-host pending" piece of std/format after format / format_bytes.
// `ms.abs()` (std/i32) is inlined as a free `i32_abs`; the duration LOGIC is
// what's covered: an if-ladder over integer div/sub/mul, `i32.to_string()` (a
// self-host builtin), string concat, and `.len()`. Each program returns the
// rendered string's length (kept <= 126) and is oracle-checked against the
// interpreter (cf. the hardcoded-expectation gap in #2908). FEATURE-AUDIT
// std/format row.
//
// `import "std/i32"` is present so the native interpreter oracle can resolve
// `.to_string()` (a self-host builtin, but a std/i32 method natively).
const formatDurationIRPrelude = `import "std/i32";
function i32_abs(n: i32): i32 { if (n < 0) { return 0 - n; } return n; }
function fmt_duration_ms(ms: i32): string {
    if (ms == 0) { return "0ms"; }
    let neg: boolean = (ms < 0);
    let mag: i32 = ms;
    if (neg) { mag = i32_abs(ms); }
    let sign: string = "";
    if (neg) { sign = "-"; }
    let h: i32 = mag / 3600000;
    let rem: i32 = mag - h * 3600000;
    let m: i32 = rem / 60000;
    rem = rem - m * 60000;
    let s: i32 = rem / 1000;
    let msPart: i32 = rem - s * 1000;
    let out: string = "";
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

// TestSelfHostFormatDurationIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostFormatDurationIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range formatDurationIRCases {
		src := formatDurationIRSrc(tc.main)
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
