package e2eselfhost

import (
	"os/exec"
	"testing"
)

// An enum call result carries one count the caller owns when the callee is an
// "ENUM:" member: every return is a fresh ctor, a forwarding call to a member,
// a local built by one that escapes only by the return, or a `var g = h.e`
// field alias, which the return retains when no credit dup'd it (#10365). A
// binding of such a call is credited like a counted enum-field alias, the
// struct and array literal consumers take the count over without a retain,
// and a produced callee's union result joins the class at the semantic
// boundary. Before, only a direct-ctor callee's rc-payload result was released,
// so every other shape here leaked its box in the caller.
//
// Each row pairs a scalar payload with an rc-payload twin, and churns a
// same-size box after every call so a box freed early reads back wrong.
const enumCallResultSrc = `enum Sc { SA(i32), SB }
enum Rc { RA(i32[]), RB }
struct HS { e: Sc, n: i32 }
struct HR { e: Rc, n: i32 }
function k_of(x: i32): i32 { return x; }
function sval(e: Sc): i32 { match (e) { SA(v) => { return v; }, SB => { return 100; } } }
function rval(e: Rc): i32 { match (e) { RA(v) => { return v[0]; }, RB => { return 100; } } }
function mk_sc_ctor(r: i32): Sc { return SA(k_of(r)); }
function mk_rc_ctor(r: i32): Rc { return RA([k_of(r), 1]); }
function mk_sc_local(r: i32): Sc {
    var m: Sc = SA(k_of(r));
    return m;
}
function mk_rc_local(r: i32): Rc {
    var m: Rc = RA([k_of(r), 1]);
    return m;
}
function mk_sc_chain(r: i32): Sc {
    var m: Sc = mk_sc_local(r);
    return m;
}
function mk_rc_chain(r: i32): Rc { return mk_rc_local(r); }
function mk_sc_branch(r: i32): Sc {
    if (r % 3 == 0) { return SB; }
    var m: Sc = SA(k_of(r));
    if (r % 3 == 1) { return m; }
    var h: HS = HS { e: SA(k_of(r + 1)), n: 1 };
    var g: Sc = h.e;
    return g;
}
function mk_rc_branch(r: i32): Rc {
    if (r % 2 == 0) {
        var h: HR = HR { e: RA([k_of(r), 7]), n: 1 };
        var g: Rc = h.e;
        return g;
    }
    return mk_rc_chain(r);
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var a: Sc = mk_sc_ctor(r);
        var b: Rc = mk_rc_ctor(r);
        var c = mk_sc_local(r);
        var d: Rc = mk_rc_local(r);
        var e: Sc = mk_sc_chain(r);
        var f: Rc = mk_rc_chain(r);
        var g: Sc = mk_sc_branch(r);
        var h: Rc = mk_rc_branch(r);
        t = t + sval(a) + rval(b) + sval(c) + rval(d) + sval(e) + rval(f) + sval(g) + rval(h);
        var hs: HS = HS { e: mk_sc_branch(r + 1), n: 1 };
        var hr: HR = HR { e: mk_rc_branch(r + 1), n: 1 };
        t = t + sval(hs.e) + rval(hr.e);
        var xs: Sc[] = [mk_sc_chain(r), mk_sc_branch(r + 2)];
        var xr: Rc[] = [mk_rc_branch(r), mk_rc_ctor(r + 3)];
        match (xs[1]) { SA(v) => { t = t + v; }, SB => { t = t + 100; } }
        match (xr[1]) { RA(v) => { t = t + v[0]; }, RB => { t = t + 100; } }
        var junk: Sc = SA(k_of(1000 + r));
        var junkr: Rc = RA([k_of(1000 + r), 2]);
        t = t + (sval(junk) + rval(junkr)) * 0;
        r = r + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// Interpreter-confirmed.
const enumCallResultWant = 64

func TestSelfHostEnumCallResultX86_64(t *testing.T) {
	checkEnumFieldAlias(t, "x86-64-linux", enumCallResultSrc, "mk_", enumCallResultWant, true)
}

func TestSelfHostEnumCallResultSanitizeX86_64(t *testing.T) {
	checkEnumFieldAliasSanitized(t, enumCallResultSrc, "mk_", enumCallResultWant)
}

func TestSelfHostEnumCallResultArm64(t *testing.T) {
	checkEnumFieldAlias(t, "arm64-linux", enumCallResultSrc, "mk_", enumCallResultWant, true)
}

func TestSelfHostEnumCallResultWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum call result")
	}
	checkEnumFieldAlias(t, "wasm32-wasi", enumCallResultSrc, "mk_", enumCallResultWant, true)
}

// A callee handing back a borrowed parameter or a parameter's field is not an
// "ENUM:" member: its result is the lender's box, uncounted. The AST lowering
// leaks the lent local, which escapes into a call that is not borrowable, so
// these rows hold the answer and the underflow detector only (#10410).
const enumCallHandbackSrc = `enum Sc { SA(i32), SB }
enum Rc { RA(i32[]), RB }
struct HS { e: Sc, n: i32 }
struct HR { e: Rc, n: i32 }
function k_of(x: i32): i32 { return x; }
function sval(e: Sc): i32 { match (e) { SA(v) => { return v; }, SB => { return 100; } } }
function rval(e: Rc): i32 { match (e) { RA(v) => { return v[0]; }, RB => { return 100; } } }
function hb_sc_param(e: Sc): Sc { return e; }
function hb_rc_param(e: Rc): Rc { return e; }
function hb_sc_field(h: HS): Sc { return h.e; }
function hb_rc_field(h: HR): Rc { return h.e; }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var a0: Sc = SA(k_of(r));
        var b0: Rc = RA([k_of(r), 1]);
        var a: Sc = hb_sc_param(a0);
        var b: Rc = hb_rc_param(b0);
        var hs: HS = HS { e: SA(k_of(r + 1)), n: 1 };
        var hr: HR = HR { e: RA([k_of(r + 1), 1]), n: 1 };
        var c: Sc = hb_sc_field(hs);
        var d: Rc = hb_rc_field(hr);
        var junk: Sc = SA(k_of(1000 + r));
        var junkr: Rc = RA([k_of(1000 + r), 2]);
        t = t + sval(a) + rval(b) + sval(c) + rval(d) + sval(a0) + rval(b0) + (sval(junk) + rval(junkr)) * 0;
        r = r + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// Interpreter-confirmed.
const enumCallHandbackWant = 24

func TestSelfHostEnumCallHandbackX86_64(t *testing.T) {
	checkEnumFieldAlias(t, "x86-64-linux", enumCallHandbackSrc, "hb_", enumCallHandbackWant, false)
}
