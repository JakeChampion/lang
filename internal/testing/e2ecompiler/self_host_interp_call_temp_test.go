package e2ecompiler

import (
	"fmt"
	"testing"
)

const interpCallPrelude = `import "std/i32";
import "std/string";
` + strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
`

func interpCallHeap(body string, limit int) string {
	return interpCallPrelude + `function round(pre: string): i32 {
` + body + `
}
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= ` + fmt.Sprint(limit) + `) { return 98; }
    return 0;
}`
}

var interpCallHeapCases = []struct {
	name string
	body string
}{
	{"interp-call-released", `    let s: string = f"{w(pre)}";
    return s.len() % 251;`},
	{"interp-two-calls-released", `    let s: string = f"{w(pre)}-{w(pre)}";
    return s.len() % 251;`},
	{"interp-explicit-tostring-released", `    let s: string = w(pre).to_string();
    return s.len() % 251;`},
	{"interp-bare-ident-control", `    let base: string = w(pre);
    let s: string = f"{base}";
    return (s.len() + base.len()) % 251;`},
	{"interp-scalar-control", `    let n: i32 = pre.len();
    let s: string = f"n={n}";
    return s.len() % 251;`},
	{"interp-explicit-concat-control", `    let s: string = pre + "-" + w(pre);
    return s.len() % 251;`},
}

var interpCallFaultCases = []struct {
	name string
	src  string
}{
	{"interp-classes-live", interpCallPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let named: string = base.to_string();
    let fresh: string = w(pre).to_string();
    let interp: string = f"{w(pre)}|{base}";
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!has_prefix(base, "abcdefgh-")) { return 0 - 1; }
    if (!has_prefix(named, "abcdefgh-")) { return 0 - 2; }
    if (!has_prefix(fresh, "abcdefgh-")) { return 0 - 3; }
    if (!has_prefix(interp, "abcdefgh-")) { return 0 - 4; }
    if (has_sub(base, "XXXX")) { return 0 - 5; }
    if (named.len() != base.len()) { return 0 - 6; }
    if (!has_sub(interp, "|")) { return 0 - 7; }
    return 3;
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { if (round(pre) != 3) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"interp-user-tostring-not-credited", interpCallPrelude + `struct Holder { name: string, tag: string }
function mk(pre: string): Holder { return Holder { name: w(pre), tag: w(pre + "t") }; }
function (h: Holder) to_string(): string { return h.name; }
function round(pre: string): i32 {
    let h: Holder = mk(pre);
    let s: string = h.to_string();
    let t: string = mk(pre).to_string();
    let p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (!has_prefix(s, "abcdefgh-")) { return 0 - 1; }
    if (!has_prefix(t, "abcdefgh-")) { return 0 - 2; }
    if (!has_prefix(h.name, "abcdefgh-")) { return 0 - 3; }
    if (has_sub(s, "XXXX")) { return 0 - 4; }
    return 3;
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { if (round(pre) != 3) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"interp-call-escape-return", interpCallPrelude + `function mk(pre: string): string { let s: string = f"{w(pre)}"; return s; }
function round(pre: string): i32 {
    let s: string = mk(pre);
    let p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (!has_prefix(s, "abcdefgh-")) { return 0 - 1; }
    return s.len() % 251;
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; let want: i32 = round(pre); while (i < 2000) { if (round(pre) != want) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
}

const interpCallExitHint = "98 = the interpolated call's box was stranded; 99 = over-release; 97 = value corrupted"

func interpCallSources() []struct{ name, src string } {
	var out []struct{ name, src string }
	for _, tc := range interpCallHeapCases {
		out = append(out, struct{ name, src string }{tc.name, interpCallHeap(tc.body, 4096)})
	}
	for _, tc := range interpCallFaultCases {
		out = append(out, struct{ name, src string }{tc.name, tc.src})
	}
	return out
}

func TestSelfHostInterpCallTempIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range interpCallSources() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, interpCallExitHint)
			}
		})
	}
}

func TestSelfHostInterpCallTempIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range interpCallSources() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, interpCallExitHint)
			}
		})
	}
}

func TestSelfHostInterpCallTempWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range interpCallSources() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, interpCallExitHint)
			}
		})
	}
}
