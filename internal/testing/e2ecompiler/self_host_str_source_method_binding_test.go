package e2ecompiler

import "testing"

// strSourceMethodBindingCases pin the reclaim credit a `let t: string = <expr>.m()`
// binding earns, and which methods must not earn it.
//
// is_fresh_ret_binding has always had a method arm, but the string collector
// passed it an empty receiver type, so the arm returned false for every binding
// in the program: `let t = b.to_owned()` leaked 47 B/round where the builtin
// `let t = b.to_ascii_upper()` beside it was flat. The struct collector next to
// it passes v.type_name and works.
//
// The credit reads a STRICT registry class rather than SFRRECV:. SFRRECV admits a
// method that returns its receiver or a view of it, which the consuming site
// sorts out with a runtime pointer compare; a binding has no discriminator and
// may only own a box that is nobody else's. Keying is by method NAME, because an
// AST scan cannot type the receiver — so a name is admitted only when every
// STRING-RETURNING declaration of it is strictly fresh, and never when it also
// spells a string builtin.
//
// The flat cases return 98 when the bound box is stranded: 400 rounds of a
// 170-byte producer is 68 KB against a 32 KB ceiling. The refusal cases are
// value-exact under same-size-class allocation pressure and verify `t` only
// through borrow positions — an earlier version compared it with `!=`, which is
// itself an escape, so nothing was credited in either direction and the probes
// distinguished nothing.
var strSourceMethodBindingCases = []struct {
	name string
	src  string
	want int
}{
	// The shape that led here, in the stdlib's spelling `(s: string) to_owned()`.
	// The whole-program freshness registry has proven such a body fresh all along;
	// the binding collector could not ask, because its method arm needs a receiver
	// type an AST scan cannot compute. 47 B/round before, flat after — the BUILTIN
	// spelling beside it was always flat.
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
	// Freshness visible only THROUGH a call, the stdlib's `(s: string) to_upper()`
	// shape (`return unicode.to_upper(s)`): admitting it needs the registry's
	// fixpoint, not just the local body. 43 B/round before.
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
	// The control the two above are measured against: a builtin-method binding, flat
	// before and after.
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
	// NEGATIVE: `return s` hands the RECEIVER's box back, so the binding is an alias
	// and freeing it double-frees what `b` still owns. Admitting it (the SFRRECV
	// test, which tolerates a receiver return) exits 99 here.
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
	// NEGATIVE: fresh on one path and the receiver on the other. One non-fresh return
	// is enough to refuse the whole method — a binding has no runtime discriminator
	// to tell the two apart, which is exactly what separates this from SFRRECV's
	// consuming-site release. Also exits 99 when admitted.
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
	// NEGATIVE: a slice return is a VIEW over the receiver's buffer — an alias with a
	// different box. Correctness only: freeing an immortal view box is not currently
	// observable, so unlike the two above this case does not fail when the rule is
	// relaxed. It pins the contract, not a witnessed fault.
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
	// NEGATIVE: a user type declaring a strictly-fresh `trim` must not license the
	// string BUILTIN of that name, which returns a view. Same correctness-only
	// standing as the case above.
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

// TestSelfHostStrSourceMethodBindingIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
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

// TestSelfHostStrSourceMethodBindingIRArm64 is the arm64 leg; the credit is shared
// lowering analysis and the release is a per-backend transcription.
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

// TestSelfHostStrSourceMethodBindingWasmIR is the wasm leg, where the release maps
// to $__fern_arr_dec on the rc-headered block.
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
