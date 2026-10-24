package e2ecompiler

import "testing"

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
	{"zero", `return fmt_duration_ms(0).len();`},
	{"sub-second", `return fmt_duration_ms(500).len();`},
	{"minutes-seconds", `return fmt_duration_ms(90000).len();`},
	{"all-units", `return fmt_duration_ms(3661001).len();`},
	{"negative", `return fmt_duration_ms(0 - 1500).len();`},
}

func formatDurationIRSrc(mainBody string) string {
	return formatDurationIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

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
