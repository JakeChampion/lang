package e2ecompiler

import "testing"

const formatSpecIRPrelude = `function fmt_is_align(c: i32): boolean {
    return c == 60 || c == 62 || c == 94;
}
function fmt_align_code(c: i32): i32 {
    if (c == 60) { return 1; }
    if (c == 62) { return 2; }
    return 3;
}
function fmt_repeat(s: string, count: i32): string {
    let out: string = "";
    let i: i32 = 0;
    while (i < count) { out = out + s; i = i + 1; }
    return out;
}
function fmt_apply_spec(s: string, spec: string): string {
    if (spec.len() == 0) { return s; }
    let m: i32 = spec.len();
    let p: i32 = 1;
    let fill: str = " ";
    let align: i32 = 1;
    if (p + 1 < m && fmt_is_align(spec[p + 1] as i32)) {
        fill = slice_unchecked(spec, p, p + 1);
        align = fmt_align_code(spec[p + 1] as i32);
        p = p + 2;
    } else if (p < m && fmt_is_align(spec[p] as i32)) {
        align = fmt_align_code(spec[p] as i32);
        p = p + 1;
    }
    let plus: boolean = false;
    if (p < m && spec[p] == 43) { plus = true; p = p + 1; }
    else if (p < m && spec[p] == 45) { p = p + 1; }
    let zero: boolean = false;
    if (p < m && spec[p] == 48) { zero = true; p = p + 1; }
    let width: i32 = 0;
    while (p < m && spec[p] >= 48 && spec[p] <= 57) {
        width = width * 10 + ((spec[p] as i32) - 48);
        p = p + 1;
    }
    let val: string = s;
    if (p < m && spec[p] == 46) {
        p = p + 1;
        let prec: i32 = 0;
        while (p < m && spec[p] >= 48 && spec[p] <= 57) {
            prec = prec * 10 + ((spec[p] as i32) - 48);
            p = p + 1;
        }
        if (val.len() > prec) { val = slice_unchecked(val, 0, prec) + ""; }
    }
    if (plus && val.len() > 0 && val[0] >= 48 && val[0] <= 57) {
        val = "+" + val;
    }
    let vlen: i32 = val.len();
    if (vlen >= width) { return val; }
    let pad: i32 = width - vlen;
    if (zero) {
        if (val.len() > 0 && (val[0] == 45 || val[0] == 43)) {
            return slice_unchecked(val, 0, 1) + fmt_repeat("0", pad) + slice_unchecked(val, 1, val.len());
        }
        return fmt_repeat("0", pad) + val;
    }
    if (align == 2) { return fmt_repeat(fill, pad) + val; }
    if (align == 3) {
        let left: i32 = pad / 2;
        return fmt_repeat(fill, left) + val + fmt_repeat(fill, pad - left);
    }
    return val + fmt_repeat(fill, pad);
}
function fmt_format(fmt: string, args: string[]): string {
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
        } else if (fmt[i] == 123) {
            let j: i32 = i + 1;
            while (j < n && fmt[j] != 125) { j = j + 1; }
            let isPlaceholder: boolean = false;
            let spec: str = "";
            if (j < n) {
                spec = slice_unchecked(fmt, i + 1, j);
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
                out = out + slice_unchecked(fmt, i, i + 1);
                i = i + 1;
            }
        } else {
            out = out + slice_unchecked(fmt, i, i + 1);
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
	{"right", `let a: string[] = ["hi"]; return fmt_format("[{:>8}]", a).len();`},
	{"left", `let a: string[] = ["hi"]; return fmt_format("[{:<8}]", a).len();`},
	{"center", `let a: string[] = ["hi"]; return fmt_format("[{:^7}]", a).len();`},
	{"fill", `let a: string[] = ["ab"]; return fmt_format("[{:*>6}]", a).len();`},
	{"precision", `let a: string[] = ["hello"]; return fmt_format("[{:.3}]", a).len();`},
	{"prec-width", `let a: string[] = ["hello"]; return fmt_format("[{:>8.3}]", a).len();`},
	{"literal-braces", `let a: string[] = []; return fmt_format("{x}", a).len();`},
	{"underflow-spec", `let a: string[] = []; return fmt_format("{:>4}", a).len();`},
	{"plain", `let a: string[] = ["x"]; return fmt_format("{}", a).len();`},
	{"sign-plus", `let a: string[] = ["42"]; return fmt_format("[{:+}]", a).len();`},
	{"sign-neg", `let a: string[] = ["-7"]; return fmt_format("[{:+}]", a).len();`},
	{"zero-pad", `let a: string[] = ["42"]; return fmt_format("[{:05}]", a).len();`},
	{"zero-pad-neg", `let a: string[] = ["-42"]; return fmt_format("[{:05}]", a).len();`},
	{"sign-zero-pad", `let a: string[] = ["42"]; return fmt_format("[{:+06}]", a).len();`},
}

func formatSpecIRSrc(mainBody string) string {
	return formatSpecIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostFormatSpecIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range formatSpecIRCases {
		src := formatSpecIRSrc(tc.main)
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
