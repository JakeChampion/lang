package e2ecompiler

import "testing"

// strSourceMethodReceiverCases pin the release of a fresh anonymous RECEIVER at a
// SOURCE-DECLARED string method, and the three callee shapes that must not get it.
//
// A fresh receiver such as `w(pre)` in `w(pre).copies()` is released after the
// call when the method carries nothing of it out; stranded, it is 46 B/round.
// A bare `return s`, a `return s[a:b]` view, and a receiver moved into a struct
// the callee hands back all carry it out, so the receiver must stay alive.
//
// The flat cases return 98 when the receiver is stranded. The three refusals all
// exit 97 when the receiver is released anyway, the VIEW case included.
var strSourceMethodReceiverCases = []struct {
	name string
	src  string
	want int
}{
	// The shape that led here: a fresh receiver at a SOURCE-DECLARED method.
	// Flat; 46 B/round when the receiver is stranded.
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
	// The method's body is irrelevant to the SITE: a result that never mentions the
	// receiver leaked 47 exactly as the copying one leaked 46. What the body decides
	// is only whether the callee-side proof admits it.
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
	// Two links, both source-declared: the inner result is the outer's fresh receiver,
	// so one admission covers both.
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
	// NEGATIVE: `return s` hands the receiver's box back, so releasing the receiver
	// frees what the caller now holds. Exits 97 when released anyway.
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
	// NEGATIVE: `return s[2:s.len()]` is a VIEW over the receiver's buffer, so the
	// receiver escapes through it. Exits 97 when released anyway.
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
	// NEGATIVE: the receiver is moved into a struct field inside the callee and read
	// back out. Nothing about the RESULT gives this away — it is a fresh-looking
	// string — so the escape check is what catches it. Also exits 97 when admitted.
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

// TestSelfHostStrSourceMethodReceiverIRX86_64 runs the cases through the self-hosted CLI for x86-64.
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

// TestSelfHostStrSourceMethodReceiverIRArm64 is the arm64 leg, run under qemu.
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

// TestSelfHostStrSourceMethodReceiverWasmIR is the wasm32-wasi leg.
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
