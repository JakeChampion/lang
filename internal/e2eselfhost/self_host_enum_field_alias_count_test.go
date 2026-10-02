package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// A local bound from an enum struct field (`let g = h.e`) shares the struct's
// enum box. The AST lowering bound it uncounted, so rebinding the struct freed
// the box under the local and the next same-size allocation read back through
// it (#10310): the census balanced and the answer was wrong. The bind now dups
// the box and the local takes the release the struct gives the same field,
// rc-gated because it is never the sole owner.
//
// Every shape churns a same-size box after the struct lets go, so a stale read
// returns the churned value. fresh_moved_into_struct and rc_shared cover the two
// releases the counted alias exposed: the scalar-enum sweep ignored a slot a
// construction had moved, and the struct re-declaration deep-dropped its enum
// field without asking whether the box was shared. peek_first reads through a
// method receiver, a source the plan cannot type.
const enumFieldAliasCountSrc = `enum Sc { SA(i32), SB }
enum Rc { RA(i32[]), RB }
struct HS { e: Sc, n: i32 }
struct HR { e: Rc, n: i32 }
function k_of(x: i32): i32 { return x; }
function sval(e: Sc): i32 { match (e) { SA(v) => { return v; }, SB => { return 100; } } }
function rval(e: Rc): i32 { match (e) { RA(v) => { return v[0]; }, RB => { return 100; } } }
function rc_loop(n: i32): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < n) {
        let h: HR = HR { e: RA([k_of(r), 1]), n: 1 };
        let g: Rc = h.e;
        h = HR { e: RB, n: 2 };
        let junk: Rc = RA([k_of(1000 + r), 2]);
        match (g) { RA(v) => { t = t + v[0]; }, RB => { t = t + 100; } }
        t = t + rval(junk) * 0;
        r = r + 1;
    }
    return t;
}
function rc_shared(n: i32): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < n) {
        let h: HR = HR { e: RA([k_of(r), 1]), n: 1 };
        let g: Rc = h.e;
        match (g) { RA(v) => { t = t + v[0]; }, RB => { t = t + 100; } }
        r = r + 1;
    }
    return t;
}
function rc_payload_kept(n: i32): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    let keep: i32[] = [];
    while (r < n) {
        let h: HR = HR { e: RA([k_of(r), 1]), n: 1 };
        let g: Rc = h.e;
        match (g) { RA(v) => { keep = v; }, RB => { t = t + 100; } }
        h = HR { e: RB, n: 2 };
        let junk: Rc = RA([k_of(1000 + r), 2]);
        t = t + keep[0] + rval(junk) * 0;
        r = r + 1;
    }
    return t;
}
function sc_once(r: i32): i32 {
    let h: HS = HS { e: SA(k_of(r)), n: 1 };
    let g = h.e;
    h = HS { e: SB, n: 2 };
    let junk: Sc = SA(k_of(1000 + r));
    return sval(g) + sval(junk) * 0;
}
function sc_shared_into_struct(r: i32): i32 {
    let h: HS = HS { e: SA(k_of(r)), n: 1 };
    let g: Sc = h.e;
    let h2: HS = HS { e: g, n: 3 };
    h = HS { e: SB, n: 2 };
    let junk: Sc = SA(k_of(1000 + r));
    return sval(h2.e) + sval(junk) * 0;
}
function fresh_moved_into_struct(r: i32): i32 {
    let m: Sc = SA(k_of(r));
    let h2: HS = HS { e: m, n: 3 };
    let junk: Sc = SA(k_of(1000 + r));
    return sval(h2.e) + sval(junk) * 0;
}
function (h: HR) peek_first(): i32 {
    let g = h.e;
    match (g) { RA(v) => { return v[0]; }, RB => { return 100; } }
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let hr: HR = HR { e: RA([k_of(r), 1]), n: 1 };
        t = t + hr.peek_first();
        let h: HS = HS { e: SA(k_of(r)), n: 1 };
        let g: Sc = h.e;
        h = HS { e: SB, n: 2 };
        let junk: Sc = SA(k_of(1000 + r));
        match (g) { SA(v) => { t = t + v; }, SB => { t = t + 100; } }
        match (junk) { SA(v) => { t = t + 0; }, SB => { t = t + 1; } }
        t = t + sc_once(r) + sc_shared_into_struct(r) + fresh_moved_into_struct(r);
        r = r + 1;
    }
    t = t + rc_loop(100) + rc_shared(100) + rc_payload_kept(100);
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// Interpreter-confirmed.
const enumFieldAliasCountWant = 24

// A returned alias (and a returned fresh enum local, the same family's sweep)
// hands its count to the caller, whose binding releases it as an "ENUM:" call
// result (#10365). Before #10356 the returning frame's own sweep freed the box
// it returned.
const enumFieldAliasReturnSrc = `enum Sc { SA(i32), SB }
enum Rc { RA(i32[]), RB }
struct HS { e: Sc, n: i32 }
struct HR { e: Rc, n: i32 }
function k_of(x: i32): i32 { return x; }
function sval(e: Sc): i32 { match (e) { SA(v) => { return v; }, SB => { return 100; } } }
function rval(e: Rc): i32 { match (e) { RA(v) => { return v[0]; }, RB => { return 100; } } }
function sc_get(r: i32): Sc {
    let h: HS = HS { e: SA(k_of(r)), n: 1 };
    let g: Sc = h.e;
    return g;
}
function rc_get(r: i32): Rc {
    let h: HR = HR { e: RA([k_of(r), 1]), n: 1 };
    let g: Rc = h.e;
    return g;
}
function fresh_get(r: i32): Sc {
    let m: Sc = SA(k_of(r));
    return m;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let a: Sc = sc_get(r);
        let b: Rc = rc_get(r);
        let c: Sc = fresh_get(r);
        let junk: Sc = SA(k_of(1000 + r));
        let junkr: Rc = RA([k_of(1000 + r), 2]);
        t = t + sval(a) + rval(b) + sval(c) + (sval(junk) + rval(junkr)) * 0;
        r = r + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

// Interpreter-confirmed.
const enumFieldAliasReturnWant = 9

// checkEnumFieldAlias runs src on target, requiring the answer and a balanced
// census.
func checkEnumFieldAlias(t *testing.T, target, src string, want int) {
	cli := buildSelfHostCLI(t)
	stderr, exit := cli.exitOf(t, src, target, "FERN_LEAKCHECK=1")
	if exit != want {
		t.Fatalf("exit = %d, want %d (99 = over-release detector)\n%s", exit, want, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostEnumFieldAliasCountX86_64(t *testing.T) {
	checkEnumFieldAlias(t, "x86-64-linux", enumFieldAliasCountSrc, enumFieldAliasCountWant)
}

// checkEnumFieldAliasSanitized runs src on x86-64 under FERN_SANITIZE,
// requiring the answer and a silent sanitizer.
func checkEnumFieldAliasSanitized(t *testing.T, src string, want int) {
	cli := buildSelfHostCLI(t)
	stderr, exit := cli.exitOf(t, src, "x86-64-linux", "FERN_SANITIZE=1")
	if exit != want || strings.Contains(stderr, "fern-sanitizer:") {
		t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, want, stderr)
	}
}

func TestSelfHostEnumFieldAliasCountSanitizeX86_64(t *testing.T) {
	checkEnumFieldAliasSanitized(t, enumFieldAliasCountSrc, enumFieldAliasCountWant)
}

func TestSelfHostEnumFieldAliasCountArm64(t *testing.T) {
	checkEnumFieldAlias(t, "arm64-linux", enumFieldAliasCountSrc, enumFieldAliasCountWant)
}

func TestSelfHostEnumFieldAliasCountWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum-field alias count")
	}
	checkEnumFieldAlias(t, "wasm32-wasi", enumFieldAliasCountSrc, enumFieldAliasCountWant)
}

func TestSelfHostEnumFieldAliasReturnX86_64(t *testing.T) {
	checkEnumFieldAlias(t, "x86-64-linux", enumFieldAliasReturnSrc, enumFieldAliasReturnWant)
}

func TestSelfHostEnumFieldAliasReturnSanitizeX86_64(t *testing.T) {
	checkEnumFieldAliasSanitized(t, enumFieldAliasReturnSrc, enumFieldAliasReturnWant)
}

func TestSelfHostEnumFieldAliasReturnArm64(t *testing.T) {
	checkEnumFieldAlias(t, "arm64-linux", enumFieldAliasReturnSrc, enumFieldAliasReturnWant)
}

func TestSelfHostEnumFieldAliasReturnWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum-field alias return")
	}
	checkEnumFieldAlias(t, "wasm32-wasi", enumFieldAliasReturnSrc, enumFieldAliasReturnWant)
}
