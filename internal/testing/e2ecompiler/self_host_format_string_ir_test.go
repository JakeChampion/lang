package e2ecompiler

import "testing"

const formatStringIRPrelude = `function fmt_format(fmt: string, args: string[]): string {
    let n: i32 = fmt.len();
    let out: string = "";
    let i: i32 = 0;
    let argi: i32 = 0;
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
	{"two-args", `let a: string[] = ["x", "yy"]; return fmt_format("a{}b{}c", a).len();`},
	{"underflow", `let a: string[] = ["x"]; return fmt_format("{}{}", a).len();`},
	{"no-placeholder", `let a: string[] = []; return fmt_format("hello", a).len();`},
	{"trailing-text", `let a: string[] = ["ab"]; return fmt_format("{}-end", a).len();`},
	{"escaped-empty", `let a: string[] = []; return fmt_format("{{}}", a).len();`},
	{"escaped-open", `let a: string[] = []; return fmt_format("a{{b", a).len();`},
	{"escape-then-arg", `let a: string[] = ["X"]; return fmt_format("{{{}}}", a).len();`},
	{"escaped-close", `let a: string[] = []; return fmt_format("x}}y", a).len();`},
}

func formatStringIRSrc(mainBody string) string {
	return formatStringIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

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
