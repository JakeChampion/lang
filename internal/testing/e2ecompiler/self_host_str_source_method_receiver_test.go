package e2ecompiler

import "testing"

var strSourceMethodReceiverCases = []struct {
	name string
	src  string
	want int
}{
	{"str-fresh-receiver-source-method-copy-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) copies(): string { return s + ""; }
function round(pre: string): i32 { let u: string = w(pre).copies(); return u.len(); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	{"str-fresh-receiver-source-method-unrelated-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) unrel(): string { if (s.len() > 0) { return "xxxxxxxxxxxxxxxxxxxx"; } return "yyyyyyyyyyyyyyyyyyyy"; }
function round(pre: string): i32 { let u: string = w(pre).unrel(); return u.len(); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	{"str-fresh-receiver-source-method-chained-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) copies(): string { return s + ""; }
function round(pre: string): i32 { return w(pre).copies().copies().len(); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	{"str-identity-return-method-receiver-refused", strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) ident(): string { return s; }
function round(pre: string): i32 {
    let t: str = w(pre).ident();
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (has_sub(t, "XXXX")) { return 0 - 2; }
    if (!has_prefix(t, "abcdefgh-a-wide")) { return 0 - 1; }
    return t.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	{"str-view-return-method-receiver-refused", strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) view(): str { return slice_unchecked(s, 2, s.len()); }
function round(pre: string): i32 {
    let t: str = w(pre).view();
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (has_sub(t, "XXXX")) { return 0 - 2; }
    if (!has_prefix(t, "cdefgh-a-wide")) { return 0 - 1; }
    return t.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 104) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	{"str-receiver-stored-in-struct-refused", strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
struct Hold { v: string }
function stash(s: string): Hold { return Hold { v: s }; }
function (s: string) keeps(): string { let h: Hold = stash(s); return h.v; }
function round(pre: string): i32 {
    let t: str = w(pre).keeps();
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (has_sub(t, "XXXX")) { return 0 - 2; }
    if (!has_prefix(t, "abcdefgh-a-wide")) { return 0 - 1; }
    return t.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
}

const strSourceMethodReceiverFailFmt = "%s = %d, want %d (98 = the receiver was stranded; 99 = over-release; 97 = value corrupted)"

func TestSelfHostStrSourceMethodReceiverIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strSourceMethodReceiverCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf(strSourceMethodReceiverFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrSourceMethodReceiverIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range strSourceMethodReceiverCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf(strSourceMethodReceiverFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrSourceMethodReceiverWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strSourceMethodReceiverCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.want {
				t.Errorf(strSourceMethodReceiverFailFmt, tc.name, code, tc.want)
			}
		})
	}
}
