package e2ecompiler

import (
	"os/exec"
	"testing"
)

// An enum call result carries one count the caller owns when the callee is an
// "ENUM:" member: every return is a fresh ctor, a forwarding call to a member,
// a local built by one that escapes only by the return, or a `let g = h.e`
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
    let m: Sc = SA(k_of(r));
    return m;
}
function mk_rc_local(r: i32): Rc {
    let m: Rc = RA([k_of(r), 1]);
    return m;
}
function mk_sc_chain(r: i32): Sc {
    let m: Sc = mk_sc_local(r);
    return m;
}
function mk_rc_chain(r: i32): Rc { return mk_rc_local(r); }
function mk_sc_branch(r: i32): Sc {
    if (r % 3 == 0) { return SB; }
    let m: Sc = SA(k_of(r));
    if (r % 3 == 1) { return m; }
    let h: HS = HS { e: SA(k_of(r + 1)), n: 1 };
    let g: Sc = h.e;
    return g;
}
function mk_rc_branch(r: i32): Rc {
    if (r % 2 == 0) {
        let h: HR = HR { e: RA([k_of(r), 7]), n: 1 };
        let g: Rc = h.e;
        return g;
    }
    return mk_rc_chain(r);
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let a: Sc = mk_sc_ctor(r);
        let b: Rc = mk_rc_ctor(r);
        let c = mk_sc_local(r);
        let d: Rc = mk_rc_local(r);
        let e: Sc = mk_sc_chain(r);
        let f: Rc = mk_rc_chain(r);
        let g: Sc = mk_sc_branch(r);
        let h: Rc = mk_rc_branch(r);
        t = t + sval(a) + rval(b) + sval(c) + rval(d) + sval(e) + rval(f) + sval(g) + rval(h);
        let hs: HS = HS { e: mk_sc_branch(r + 1), n: 1 };
        let hr: HR = HR { e: mk_rc_branch(r + 1), n: 1 };
        t = t + sval(hs.e) + rval(hr.e);
        let xs: Sc[] = [mk_sc_chain(r), mk_sc_branch(r + 2)];
        let xr: Rc[] = [mk_rc_branch(r), mk_rc_ctor(r + 3)];
        match (xs[1]) { SA(v) => { t = t + v; }, SB => { t = t + 100; } }
        match (xr[1]) { RA(v) => { t = t + v[0]; }, RB => { t = t + 100; } }
        let junk: Sc = SA(k_of(1000 + r));
        let junkr: Rc = RA([k_of(1000 + r), 2]);
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
	checkEnumFieldAlias(t, "x86-64-linux", enumCallResultSrc, enumCallResultWant)
}

func TestSelfHostEnumCallResultSanitizeX86_64(t *testing.T) {
	checkEnumFieldAliasSanitized(t, enumCallResultSrc, enumCallResultWant)
}

func TestSelfHostEnumCallResultArm64(t *testing.T) {
	checkEnumFieldAlias(t, "arm64-linux", enumCallResultSrc, enumCallResultWant)
}

func TestSelfHostEnumCallResultWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum call result")
	}
	checkEnumFieldAlias(t, "wasm32-wasi", enumCallResultSrc, enumCallResultWant)
}

// A callee handing back a borrowed parameter, or a borrowed struct parameter's
// enum field, is an "ENUM:" member too: the return retains what it hands back,
// so the result carries the caller's count (#10410). A lend at such a position
// is a counted store, so the lender keeps its own release, and a temporary
// argument there is released after the call. The last rows put a handback
// result in each temporary position.
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
function hb_rc_fwd(e: Rc): Rc { return hb_rc_param(e); }
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let a0: Sc = SA(k_of(r));
        let b0: Rc = RA([k_of(r), 1]);
        let a: Sc = hb_sc_param(a0);
        let b: Rc = hb_rc_param(b0);
        let hs: HS = HS { e: SA(k_of(r + 1)), n: 1 };
        let hr: HR = HR { e: RA([k_of(r + 1), 1]), n: 1 };
        let c: Sc = hb_sc_field(hs);
        let d: Rc = hb_rc_field(hr);
        let f: Rc = hb_rc_fwd(b0);
        let junk: Sc = SA(k_of(1000 + r));
        let junkr: Rc = RA([k_of(1000 + r), 2]);
        t = t + sval(a) + rval(b) + sval(c) + rval(d) + rval(f) + sval(a0) + rval(b0) + (sval(junk) + rval(junkr)) * 0;
        t = t + sval(hb_sc_param(SA(k_of(r + 2)))) + rval(hb_rc_param(RA([k_of(r + 2), 1])));
        t = t + rval(hb_rc_fwd(RA([k_of(r + 3), 1])));
        match (hb_rc_param(b0)) { RA(v) => { t = t + v[0]; }, RB => { t = t + 100; } }
        match (hb_sc_field(hs)) { SA(v) => { t = t + v; }, SB => { t = t + 100; } }
        hb_sc_param(a0);
        hb_rc_field(hr);
        let junk2: Rc = RA([k_of(2000 + r), 2]);
        t = t + rval(junk2) * 0;
        r = r + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// Interpreter-confirmed.
const enumCallHandbackWant = 66

func TestSelfHostEnumCallHandbackX86_64(t *testing.T) {
	checkEnumFieldAlias(t, "x86-64-linux", enumCallHandbackSrc, enumCallHandbackWant)
}

func TestSelfHostEnumCallHandbackSanitizeX86_64(t *testing.T) {
	checkEnumFieldAliasSanitized(t, enumCallHandbackSrc, enumCallHandbackWant)
}

func TestSelfHostEnumCallHandbackArm64(t *testing.T) {
	checkEnumFieldAlias(t, "arm64-linux", enumCallHandbackSrc, enumCallHandbackWant)
}

func TestSelfHostEnumCallHandbackWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum call handback")
	}
	checkEnumFieldAlias(t, "wasm32-wasi", enumCallHandbackSrc, enumCallHandbackWant)
}

// A local lent to a handback callee and then returned (#10443). The AST
// lowering credited the local's release while the returned result was the same
// box uncounted, so the churned box was read back: 93 instead of 3.
const enumHandbackReturnSrc = `enum Sc { SA(i32), SB }
function k_of(x: i32): i32 { return x; }
function sval(e: Sc): i32 { match (e) { SA(v) => { return v; }, SB => { return 100; } } }
function hb_sc_param(e: Sc): Sc { return e; }
function mk(r: i32): Sc {
    let a0: Sc = SA(k_of(r));
    let a: Sc = hb_sc_param(a0);
    return a;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let x: Sc = mk(r);
        let junk: Sc = SA(k_of(1000 + r));
        t = t + sval(x) + sval(junk) * 0;
        r = r + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// Interpreter-confirmed.
const enumHandbackReturnWant = 3

func TestSelfHostEnumHandbackReturnX86_64(t *testing.T) {
	checkEnumFieldAlias(t, "x86-64-linux", enumHandbackReturnSrc, enumHandbackReturnWant)
}

func TestSelfHostEnumHandbackReturnSanitizeX86_64(t *testing.T) {
	checkEnumFieldAliasSanitized(t, enumHandbackReturnSrc, enumHandbackReturnWant)
}

func TestSelfHostEnumHandbackReturnArm64(t *testing.T) {
	checkEnumFieldAlias(t, "arm64-linux", enumHandbackReturnSrc, enumHandbackReturnWant)
}

func TestSelfHostEnumHandbackReturnWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum handback return")
	}
	checkEnumFieldAlias(t, "wasm32-wasi", enumHandbackReturnSrc, enumHandbackReturnWant)
}

// An "ENUM:" member's result in each temporary position: an argument, a match
// scrutinee and a discarded call, for a callee returning a ctor and one
// returning a local, with scalar and rc payloads. Each position released
// nothing before, apart from a scalar-payload argument and an "RCE:" ctor
// callee's argument and rc-payload scrutinee.
const enumCallTempSrc = `enum Sc { SA(i32), SB }
enum Rc { RA(i32[]), RB }
function k_of(x: i32): i32 { return x; }
function sval(e: Sc): i32 { match (e) { SA(v) => { return v; }, SB => { return 100; } } }
function rval(e: Rc): i32 { match (e) { RA(v) => { return v[0]; }, RB => { return 100; } } }
function mk_sc_ctor(r: i32): Sc { return SA(k_of(r)); }
function mk_rc_ctor(r: i32): Rc { return RA([k_of(r), 1]); }
function mk_sc_local(r: i32): Sc {
    let m: Sc = SA(k_of(r));
    return m;
}
function mk_rc_local(r: i32): Rc {
    let m: Rc = RA([k_of(r), 1]);
    return m;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        t = t + sval(mk_sc_ctor(r)) + rval(mk_rc_ctor(r)) + sval(mk_sc_local(r)) + rval(mk_rc_local(r));
        match (mk_sc_ctor(r + 1)) { SA(v) => { t = t + v; }, SB => { t = t + 100; } }
        match (mk_rc_ctor(r + 1)) { RA(v) => { t = t + v[0]; }, RB => { t = t + 100; } }
        match (mk_sc_local(r + 2)) { SA(v) => { t = t + v; }, SB => { t = t + 100; } }
        match (mk_rc_local(r + 2)) { RA(v) => { t = t + v[0]; }, RB => { t = t + 100; } }
        mk_sc_ctor(r);
        mk_rc_ctor(r);
        mk_sc_local(r);
        mk_rc_local(r);
        let junk: Sc = SA(k_of(1000 + r));
        let junkr: Rc = RA([k_of(1000 + r), 2]);
        t = t + (sval(junk) + rval(junkr)) * 0;
        r = r + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// Interpreter-confirmed.
const enumCallTempWant = 42

func TestSelfHostEnumCallTempX86_64(t *testing.T) {
	checkEnumFieldAlias(t, "x86-64-linux", enumCallTempSrc, enumCallTempWant)
}

func TestSelfHostEnumCallTempSanitizeX86_64(t *testing.T) {
	checkEnumFieldAliasSanitized(t, enumCallTempSrc, enumCallTempWant)
}

func TestSelfHostEnumCallTempArm64(t *testing.T) {
	checkEnumFieldAlias(t, "arm64-linux", enumCallTempSrc, enumCallTempWant)
}

func TestSelfHostEnumCallTempWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum call temporaries")
	}
	checkEnumFieldAlias(t, "wasm32-wasi", enumCallTempSrc, enumCallTempWant)
}
