package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The heap-box match-scrutinee reclaim refused the WHOLE match as soon as any
// arm bound a pointer-shaped payload, so `match (mk()) { Ok(s) => { … } }` over
// a fresh call leaked the box and its payload every iteration — the same shape
// #5339 fixed for the pair-form ABI, on the boxed side.
//
// The gate already excused `_`, on the grounds that it "extracts nothing, so no
// binding outlives the free". A NAMED binding the arm cannot let escape has
// that property too; it just has to be proven rather than read off the
// spelling. bindingConfinedToArm is that proof — the same whitelist the
// pair-form payload release has been gated on.
//
// Measured on the discriminator that isolates it, 20 rounds of a three-variant
// enum with a fresh string payload:
//
//	BOk(_)  -> allocs=40 frees=40   (flat: the gate excused it)
//	BOk(s)  -> allocs=40 frees=0    (everything leaked)
//
// The reclaim is a DEEP drop, so admitting the arm releases the payload as well
// as the box.
//
// The sources compare their own two round-blocks and return 98 rather than
// handing the byte delta back as an exit status: a status is masked to 0..255,
// and the leak here is 320000 bytes at the larger N — which reads as exactly 0.
// The first draft of this test did that, and its "N=5000 -> 0" is what gave it
// away.

func boxedPtrPayloadStrSrc() string {
	return `enum BxS { BOk(string), BErr(i32), BNone }
function tag(v: i32): string { if (v == 0) { return "aa"; } if (v == 1) { return "bb"; } return "cc"; }
function mk(k: i32): BxS { if (k < 0) { return BErr(1); } return BOk("box-payload-" + tag(k)); }
function take(k: i32): i32 { match (mk(k)) { BOk(s) => { return s.len(); }, BErr(e) => { return e; }, BNone => { return 0; } } return 0; }
function rounds(n: i32): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < n) { acc = acc + take(i % 3); i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = rounds(50);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var y: i32 = rounds(500);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (x + y < 0) { return 97; }
    if ((b2 - b1) > (b1 - b0)) { return 98; }
    return 0;
}`
}

func boxedPtrPayloadArrSrc() string {
	return `enum BxA { AOk(i32[]), AErr(i32), ANone }
function mk(k: i32): BxA { if (k < 0) { return AErr(1); } return AOk([k, k + 1, k + 2]); }
function take(k: i32): i32 { match (mk(k)) { AOk(a) => { return a.len(); }, AErr(e) => { return e; }, ANone => { return 0; } } return 0; }
function rounds(n: i32): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < n) { acc = acc + take(i % 3); i = i + 1; }
    return acc;
}
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = rounds(50);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var y: i32 = rounds(500);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (x + y < 0) { return 97; }
    if ((b2 - b1) > (b1 - b0)) { return 98; }
    return 0;
}`
}

// The expression form of the same shape — a separate call site with its own
// result-type gate, so it needs its own row.
func boxedPtrPayloadExprSrc() string {
	return `enum BxS { BOk(string), BErr(i32), BNone }
function tag(v: i32): string { if (v == 0) { return "aa"; } if (v == 1) { return "bb"; } return "cc"; }
function mk(k: i32): BxS { if (k < 0) { return BErr(1); } return BOk("box-payload-" + tag(k)); }
function rounds(n: i32): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < n) {
        var r: i32 = match (mk(i % 3)) { BOk(s) => s.len(), BErr(e) => e, BNone => 0 };
        acc = acc + r;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = rounds(50);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var y: i32 = rounds(500);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (x + y < 0) { return 97; }
    if ((b2 - b1) > (b1 - b0)) { return 98; }
    return 0;
}`
}

// CRITICAL soundness. Every arm here MUST be refused, and each one escapes the
// payload by a different route: returned outright, assigned to an outer local,
// pushed into a container, held by an `@` binding on the box, named in a guard,
// and — the freshness gate rather than the confinement one — a scrutinee that
// is a parameter pass-through whose source is read again afterwards.
//
// Every escaped value is READ after the match, so freeing any of them is a
// use-after-free (a wrong length, or a trap) rather than a byte count. The
// total is pinned against the interpreter, which shares none of this lowering.
const boxedPtrPayloadEscapeSafe = `import "core/int";
import "std/i32";
enum BxS { BOk(string), BErr(i32), BNone }
function tag(v: i32): string { if (v == 0) { return "aa"; } if (v == 1) { return "bb"; } return "cc"; }
function mk(k: i32): BxS { if (k < 0) { return BErr(1); } return BOk("box-payload-" + tag(k)); }
function pass(b: BxS): BxS { return b; }
function h1(k: i32): string { match (mk(k)) { BOk(s) => { return s; }, BErr(e) => { return "e"; }, BNone => { return "n"; } } return "x"; }
function h2(k: i32): i32 {
    var out: string = "";
    match (mk(k)) { BOk(s) => { out = s; }, BErr(e) => {}, BNone => {} }
    return out.len();
}
function h3(k: i32): i32 {
    var keep: string[] = [];
    match (mk(k)) { BOk(s) => { keep = keep.append(s); }, BErr(e) => {}, BNone => {} }
    if (keep.len() == 0) { return 0; }
    return keep[0].len();
}
function h4(k: i32): i32 {
    var n: i32 = 0;
    match (mk(k)) { w @ BOk(s) => { n = s.len(); match (w) { BOk(t) => { n = n + t.len(); }, BErr(_) => {}, BNone => {} } }, BErr(e) => { n = e; }, BNone => {} }
    return n;
}
function h5(k: i32): i32 { match (mk(k)) { BOk(s) when s.len() > 2 => { return s.len(); }, BOk(s2) => { return 1; }, BErr(e) => { return e; }, BNone => { return 0; } } return 0; }
function h6(k: i32): i32 {
    var b: BxS = mk(k);
    var n: i32 = 0;
    match (pass(b)) { BOk(s) => { n = s.len(); }, BErr(e) => { n = e; }, BNone => {} }
    match (b) { BOk(t) => { n = n + t.len(); }, BErr(_) => {}, BNone => {} }
    return n;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 60) {
        var k: i32 = i % 3;
        t = t + h1(k).len() + h2(k) + h3(k) + h4(k) + h5(k) + h6(k);
        i = i + 1;
    }
    if (t != 6720) { return 99; }
    return __rc_underflow_count();
}`

func checkBoxedPtrPayloadSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, boxedPtrPayloadEscapeSafe); code != 0 {
		t.Errorf("escaping payload: code=%d (99=value/UAF, >0=over-release)", code)
	}
}

var boxedPtrPayloadSrcs = []func() string{
	boxedPtrPayloadStrSrc,
	boxedPtrPayloadArrSrc,
	boxedPtrPayloadExprSrc,
}

func TestX86_64BoxedScrutineePtrPayloadReclaim(t *testing.T) {
	for _, mk := range boxedPtrPayloadSrcs {
		if _, code := compileAndRunX86_64FreeOn(t, mk()); code != 0 {
			t.Errorf("boxed pointer-payload: code=%d (98=grows, 97=value)", code)
		}
	}
	checkBoxedPtrPayloadSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64BoxedScrutineePtrPayloadReclaim(t *testing.T) {
	for _, mk := range boxedPtrPayloadSrcs {
		if _, code := compileAndRunArm64FreeOn(t, mk()); code != 0 {
			t.Errorf("boxed pointer-payload: code=%d (98=grows, 97=value)", code)
		}
	}
	checkBoxedPtrPayloadSafe(t, compileAndRunArm64FreeOn)
}

func TestWASMBoxedScrutineePtrPayloadReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, mk := range boxedPtrPayloadSrcs {
		if code := runWasm(t, mk()); code != 0 {
			t.Errorf("boxed pointer-payload: code=%d (98=grows, 97=value)", code)
		}
	}
}
