package e2ecompiler

import "testing"

var strSourceMethodBindingCases = []struct {
	name string
	src  string
	want int
}{
	{"str-source-method-binding-reclaimed", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-string-dominates-0123456789"; }
function (s: string) owned(): string { return s + ""; }
function round(pre: string): i32 { let b: string = w(pre); let t: string = b.owned(); return t.len(); }
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
	{"str-source-method-transitive-binding-reclaimed", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-string-dominates-0123456789"; }
function widen(s: string): string { return s + ""; }
function (s: string) up(): string { return widen(s); }
function round(pre: string): i32 { let b: string = w(pre); let t: string = b.up(); return t.len(); }
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
	{"str-builtin-method-binding-reclaimed", `import "std/string";
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-string-dominates-0123456789"; }
function round(pre: string): i32 { let b: string = w(pre); let t: string = b.to_ascii_upper(); return t.len(); }
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
	{"str-identity-return-method-binding-refused", strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-string-dominates-0123456789"; }
function (s: string) same(): string { return s; }
function round(pre: string): i32 {
    let b: string = w(pre);
    let t: string = b.same();
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (!has_prefix(t, "abcdefgh-a-wide")) { return 0 - 1; }
    if (has_sub(t, "XXXX")) { return 0 - 2; }
    return t.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 113) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	{"str-mixed-path-method-binding-refused", strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-string-dominates-0123456789"; }
function (s: string) maybe(k: i32): string { if (k > 0) { return s + "!"; } return s; }
function round(pre: string): i32 {
    let b: string = w(pre);
    let t: string = b.maybe(0);
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (!has_prefix(t, "abcdefgh-a-wide")) { return 0 - 1; }
    if (has_sub(t, "XXXX")) { return 0 - 2; }
    return t.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 113) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	{"str-view-return-method-binding-refused", strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-string-dominates-0123456789"; }
function (s: string) rest(): str { return slice_unchecked(s, 2, s.len()); }
function round(pre: string): i32 {
    let b: string = w(pre);
    let t: str = b.rest();
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (!has_prefix(t, "cdefgh-a-wide")) { return 0 - 1; }
    if (has_sub(t, "XXXX")) { return 0 - 2; }
    return t.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 111) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	{"str-builtin-name-collision-binding-refused", "import \"std/string\";\n" + strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-string-dominates-0123456789"; }
struct Box { v: string }
function (x: Box) trim(): string { return x.v + ""; }
function round(pre: string): i32 {
    let b: string = w(pre);
    let t: str = b.trim();
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (!has_prefix(t, "abcdefgh-a-wide")) { return 0 - 1; }
    if (has_sub(t, "XXXX")) { return 0 - 2; }
    return t.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 113) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
}

const strSourceMethodBindingExitHint = "98 = the bound box was stranded; 99 = over-release; 97 = value corrupted"

func TestSelfHostStrSourceMethodBindingIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strSourceMethodBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf("%s = %d, want %d (%s)", tc.name, code, tc.want, strSourceMethodBindingExitHint)
			}
		})
	}
}

func TestSelfHostStrSourceMethodBindingIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range strSourceMethodBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("%s = %d, want %d (%s)", tc.name, code, tc.want, strSourceMethodBindingExitHint)
			}
		})
	}
}

func TestSelfHostStrSourceMethodBindingWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strSourceMethodBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("%s = %d, want %d (%s)", tc.name, got, tc.want, strSourceMethodBindingExitHint)
			}
		})
	}
}
