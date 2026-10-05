package e2eselfhost

import "testing"

// strBindSfrrecvCases pin the release of a FRESH-OR-RECEIVER method result in
// BINDING position — `let v: str = base.drop(2)`, the half of #6544 the receiver
// position already had.
//
// SFRRECV admits a method whose every return is fresh, the bare RECEIVER, or a
// view of it, so no static answer says which one a call produced: `drop(0)` hands
// base's own box back and `drop(2)` allocates a view over base's bytes. The
// binding therefore carries the same runtime discriminator the `.len()` site
// uses — release only when the result differs from the chain ROOT's pointer —
// and __fern_str_view_free, so a view box is returned to the freelist without
// touching the shared data.
//
// A stranded view box is only 24 B/round, so the heap harness requires the
// second churn to leave the heap bump flat rather than under a threshold.
const strBindPrelude = `import "std/i32";
import "std/i64";
import "std/string";
` + strProbeHelpers + `struct Holder { name: string }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) tail(n: i32): str {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function (s: string) pad2(n: i32): string {
    if (n <= 0) { return s; }
    return s + "0123456789abcdef0123456789abcdef0123456789abcdef";
}
function (s: string) to_owned2(): string { return s + ""; }
function (s: string) ident2(): string { return s; }
`

// strBindHeap wraps a `round` body in the churn/heap-delta harness. 98 means the
// bound result was stranded.
func strBindHeap(round string) string {
	return strBindPrelude + `function round(pre: string): i32 { let base: string = w(pre); ` + round + ` }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 != b1) { return 98; }
    return 0;
}`
}

var strBindSfrrecvCases = []struct {
	name string
	src  string
	want int
}{
	// The issue's shape. `tail(4)` returns a 24-byte view box over base's bytes
	// that nothing else names; 24 B/round before, flat after.
	{"str-bind-sfrrecv-view-flat", strBindHeap(`let v: str = base.tail(4); return v.len();`), 0},
	// The same admission carrying a box that owns its data: `pad2`'s non-identity
	// return is a concat. The view-aware free takes __fern_str_free's path on it,
	// which is what makes one release right for both shapes.
	{"str-bind-sfrrecv-owned-flat", strBindHeap(`let v: string = base.pad2(4); return v.len();`), 0},
	// The BINDING carries no annotation. Only the RECEIVER's declared type is
	// consulted, so this is admitted exactly like the annotated form.
	{"str-bind-sfrrecv-unannotated-flat", strBindHeap(`let v = base.tail(4); return v.len();`), 0},
	// CONTROL: an outer link whose every return is fresh is already credited by
	// str_method_ret_is_fresh ("SFRFRESHNAME:"), so this was flat before. It pins
	// that the new credit does not stack a second release onto that one.
	{"str-bind-fresh-name-chain-control", strBindHeap(`let v: string = base.tail(4).to_owned2(); return v.len();`), 0},
	// A PARAM root: params carry no `let` in the body, and the declared-name/type
	// pair has to reach them or every `function f(p: str)` is refused.
	{"str-bind-sfrrecv-param-root-flat", strBindPrelude + `function inner(p: string): i32 { let v: str = p.tail(4); return v.len(); }
function round(pre: string): i32 { let base: string = w(pre); return inner(base); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 != b1) { return 98; }
    return 0;
}`, 0},
	// WITNESS for the pointer compare, and the case that fails in the OPPOSITE
	// direction from the byte gates above. `tail(0)` takes the identity path, so
	// the bound value IS base's box: a compiler that keeps the credit and drops
	// the compare frees what `base` still holds, and base's own sweep then
	// double-frees. Verified: 99 (rc underflow) on x86-64 and on wasm with the
	// compare removed, 0 with it.
	{"str-bind-sfrrecv-identity-guarded", strBindPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let c: str = base.tail(0);
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (has_sub(base, "XXXX")) { return 0 - 1; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 2; }
    if (!has_prefix(c, "abcdefgh-a-wide")) { return 0 - 3; }
    return base.len() + c.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 212) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// A two-link CHAIN, and the same witness one link deeper: the walk has to see
	// through the inner call to reach base, the inner link takes its identity path
	// and the outer allocates over base's bytes, so the result is NOT base's box
	// and the release must run. Holds the two halves of the compare against each
	// other — a compiler that skipped the release wholesale would pass the case
	// above and strand this one.
	//
	// The inner link is `tail(0)` rather than `tail(4)` because an inner link that
	// ALLOCATES strands its own box either way: releasing an intermediate needs
	// the outer callee proven borrowing, which is a separate slice.
	{"str-bind-sfrrecv-chain-identity-link-flat", strBindHeap(`let v: str = base.tail(0).tail(2); return v.len();`), 0},
	// REFUSED: a FIELD root. The credit needs a slot the frame still holds to
	// compare against, and `h.name` is not one — crediting it anyway would free
	// the field's own box on the identity path, which the reads below catch.
	{"str-bind-sfrrecv-field-root-refused", strBindPrelude + `function round(pre: string): i32 {
    let h: Holder = Holder { name: w(pre) };
    let c: str = h.name.tail(0);
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!has_prefix(h.name, "abcdefgh-a-wide")) { return 0 - 2; }
    if (!has_prefix(c, "abcdefgh-a-wide")) { return 0 - 3; }
    return h.name.len() + c.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 212) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// REFUSED: the method hands its receiver back on EVERY path, so it is not in
	// the registry at all and the binding is a plain borrow. Pins that the credit
	// is keyed on the whole-program proof, not on the shape of the call.
	{"str-bind-identity-method-refused", strBindPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let c: string = base.ident2();
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 2; }
    if (!has_prefix(c, "abcdefgh-a-wide")) { return 0 - 3; }
    return base.len() + c.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 212) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// LIVENESS for the released shape: the view has to read correctly after the
	// call and after unrelated allocations, and base has to survive the release of
	// a box built over its bytes.
	{"str-bind-sfrrecv-view-liveness", strBindPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let v: str = base.tail(9);
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 2; }
    if (!has_prefix(v, "a-wide-payload")) { return 0 - 3; }
    return base.len() + v.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 203) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// A STATIC root and the empty-literal return path in one round: `tail(9999)`
	// hands back `""`, whose box is in .rodata and whose pointer differs from the
	// root, so the compare admits it and the release runs on a static. That is
	// safe because __fern_str_view_free's view case guards on the box base being
	// inside the arena. The view over the literal's bytes is the other half — the
	// data pointer is below heap_base and must survive the box release.
	{"str-bind-sfrrecv-literal-root-liveness", strBindPrelude + `function round(): i32 {
    let base: string = "abcdefgh-a-static-literal-in-rodata-0123456789";
    let v: str = base.tail(2);
    let e: str = base.tail(9999);
    if (!has_prefix(base, "abcdefgh")) { return 0 - 1; }
    if (!has_prefix(v, "cdefgh")) { return 0 - 2; }
    if (e.len() != 0) { return 0 - 3; }
    return v.len() + base.len();
}
function main(): i32 { let i: i32 = 0; while (i < 4000) { let r: i32 = round(); if (r != 90) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// The credit is keyed by NAME, so a second `let v` in another block shares it
	// while holding a plain alias — here a struct FIELD, which no slot compare can
	// name. Both the alias and the field it reads have to survive the sweep.
	{"str-bind-sfrrecv-same-name-alias-liveness", strBindPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let h: Holder = Holder { name: w(pre) };
    let total: i32 = 0;
    if (base.len() > 0) { let v: str = base.tail(2); total = total + v.len(); }
    if (base.len() > 0) { let v: str = h.name; total = total + v.len(); }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 1; }
    if (!has_prefix(h.name, "abcdefgh-a-wide")) { return 0 - 2; }
    return total;
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 210) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// REFUSED: the binding ESCAPES (it is returned), so the shared escape gate
	// withholds the credit and the caller keeps a live box.
	{"str-bind-sfrrecv-escaping-refused", strBindPrelude + `function mk(pre: string): string { let base: string = w(pre); let v: string = base.pad2(4); return v; }
function round(pre: string): i32 {
    let r: string = mk(pre);
    let p1: string = w("ZZZZZZZZ");
    if (p1.len() < 0) { return 0; }
    if (!has_prefix(r, "abcdefgh-a-wide")) { return 0 - 2; }
    if (!has_suffix(r, "0123456789abcdef")) { return 0 - 3; }
    return r.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 154) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrBindSfrrecvIRX86_64 drives the cases through the self-hosted
// CLI for x86-64.
func TestSelfHostStrBindSfrrecvIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strBindSfrrecvCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the bound result was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// strBindStdlibCases run the real `std/string` through the module loader, where
// the registry that admits `drop` is built from a SIBLING module's declarations.
//
// These are the helpers the issue is about.
var strBindStdlibCases = []struct {
	name string
	call string
	want int
}{
	{"stdlib-drop-view", `base.drop(2)`, 0},
	{"stdlib-pad-start-owned", `base.pad_start(200, " ")`, 0},
	{"stdlib-remove-prefix-view", `base.remove_prefix("abcd")`, 0},
	// The identity path of the same helper, guarded: `drop(0)` returns base's own
	// box, and base is read after. 99/97 without the pointer compare.
	{"stdlib-drop-identity-guarded", `base.drop(0)`, 0},
}

// strBindStdlibMain wraps a stdlib call in the churn/heap harness, reading both
// the binding and its receiver each round so an over-release shows as 97/99 and a
// stranded box as 98.
func strBindStdlibMain(call string) string {
	return `import "std/string";
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 {
    let base: string = w(pre);
    let v: str = ` + call + `;
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    return v.len();
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
    if (b2 != b1) { return 98; }
    return 0;
}
`
}

// TestSelfHostStrBindSfrrecvStdlibX86_64 is the cross-module leg: the same credit
// on the real std/string helpers, loaded as a sibling module.
func TestSelfHostStrBindSfrrecvStdlibX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strBindStdlibCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", strBindStdlibMain(tc.call))); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the bound result was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrBindSfrrecvIRArm64 is the arm64 leg; the admission and the
// pointer compare are in the shared lowering, the release a per-backend transcription.
func TestSelfHostStrBindSfrrecvIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range strBindSfrrecvCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the bound result was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrBindSfrrecvWasmIR is the wasm leg, where __fern_str_view_free
// maps to $__fern_arr_dec — wasm slices copy, so every admitted result is an
// ordinary owned block.
func TestSelfHostStrBindSfrrecvWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strBindSfrrecvCases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("%s = %d, want %d (98 = the bound result was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
