package e2ecompiler

import "testing"

// tryDeferIRCases pin defer/errdefer firing on the `?` (try) FAILURE path
// (#4334): plain defers first, then errdefers, then the reclaim of owned
// locals, in that order. Every case the interpreter can judge is cross-checked
// against it before asserting the self-hosted CLI's result.
var tryDeferIRCases = []struct {
	name    string
	main    string
	wantOut string
	want    int
	// bumpMark rows take their verdict from a bump-mark difference, which the
	// interpreter cannot see (its `__heap_bump_bytes` is a constant 0), so
	// they are their own oracle.
	bumpMark bool
}{
	// Both defer and errdefer fire on a failing `?`, defer first — then the
	// caller sees the propagated Err.
	{"result-fail-defer-and-errdefer",
		`function step(x: i32): Result[i32, i32] { if (x < 0) { return Err(1); } return Ok(x); }
function f(x: i32): Result[i32, i32] {
    defer print("D");
    errdefer print("E");
    let v: i32 = step(x)?;
    return Ok(v);
}
function main(): i32 {
    match (f(0 - 1)) { Ok(v) => {}, Err(e) => { print("err"); } }
    return 0;
}`, "D\nE\nerr\n", 0, false},
	// Success path: the `?` yields the payload, the function leaves via its
	// normal return — defer fires there, errdefer stays silent.
	{"success-defer-at-return-only",
		`function step(x: i32): Result[i32, i32] { if (x < 0) { return Err(1); } return Ok(x); }
function f(x: i32): Result[i32, i32] {
    defer print("D");
    errdefer print("E");
    let v: i32 = step(x)?;
    return Ok(v);
}
function main(): i32 {
    match (f(5)) { Ok(v) => { print("ok"); }, Err(e) => { print("err"); } }
    return 0;
}`, "D\nok\n", 0, false},
	// Two defers replay LIFO at the `?` edge, same as at an explicit return.
	{"lifo-order-at-try-edge",
		`function step(x: i32): Result[i32, i32] { if (x < 0) { return Err(1); } return Ok(x); }
function f(x: i32): Result[i32, i32] {
    defer print("A");
    defer print("B");
    let v: i32 = step(x)?;
    return Ok(v);
}
function main(): i32 { match (f(0 - 3)) { Ok(v) => {}, Err(e) => { print("err"); } } return 0; }`,
		"B\nA\nerr\n", 0, false},
	// Registration is dynamic: a conditionally-registered defer fires only when
	// its branch ran, and a defer AFTER the `?` never fires on the failure path.
	{"conditional-and-late-registration",
		`function step(x: i32): Result[i32, i32] { if (x < 0) { return Err(1); } return Ok(x); }
function f(reg: boolean, x: i32): Result[i32, i32] {
    if (reg) { defer print("C"); }
    let v: i32 = step(x)?;
    defer print("L");
    return Ok(v);
}
function main(): i32 {
    match (f(true, 0 - 1)) { Ok(v) => {}, Err(e) => { print("e1"); } }
    match (f(false, 0 - 1)) { Ok(v) => {}, Err(e) => { print("e2"); } }
    return 0;
}`, "C\ne1\ne2\n", 0, false},
	// Option `?`: None propagation is the error path too — both kinds fire.
	{"option-none-propagation",
		`function step(x: i32): Option[i32] { if (x < 0) { return None; } return Some(x); }
function f(x: i32): Option[i32] {
    defer print("D");
    errdefer print("E");
    let v: i32 = step(x)?;
    return Some(v + 1);
}
function main(): i32 { match (f(0 - 2)) { Some(v) => {}, None => { print("none"); } } return 0; }`,
		"D\nE\nnone\n", 0, false},
	// RC interplay: with a defer registered, an owned local live at the `?` is
	// still reclaimed on the failure path. The bare loop is the baseline; the
	// owned loop's extra heap growth stays ~0 only if the array is freed.
	{"rc-owned-array-reclaimed-with-defer",
		`function fails(): Option[i32] { return None; }
function step_bare(): Option[i32] { let acc: i32 = 0; defer acc = acc + 1; let x: i32 = fails()?; return Some(x); }
function step_owned(): Option[i32] { let acc: i32 = 0; defer acc = acc + 1; let owned: i32[] = [1, 2, 3, 4, 5]; let x: i32 = fails()?; return Some(x + owned[0]); }
function main(): i32 {
    let b0: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 20000) { match (step_bare()) { Some(_) => {}, None => {} } i = i + 1; }
    let base: i32 = (__heap_bump_bytes() as i32) - b0;
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 20000) { match (step_owned()) { Some(_) => {}, None => {} } j = j + 1; }
    if ((__heap_bump_bytes() as i32) - b1 - base < 100000) { return 7; }
    return 1;
}`, "", 7, true},
	// A defer reading the binding a `?` produces (#11845): the `?` edge
	// replays only the defers armed before it, so it never names `x`.
	{"defer-reads-binding-after-try",
		`function get(ok: boolean): Option[i32] { if (ok) { return Some(1); } return None; }
function show(n: i32): void { if (n == 1) { print("deferred"); } }
function f(ok: boolean): Option[i32] {
    let x: i32 = get(ok)?;
    defer show(x);
    return Some(x);
}
function main(): i32 {
    match (f(true)) { Some(v) => { print("some"); }, None => { print("none"); } }
    match (f(false)) { Some(v) => { print("some"); }, None => { print("none"); } }
    return 0;
}`, "deferred\nsome\nnone\n", 0, false},
	// The resource pattern with unannotated bindings: each `?` replays the
	// closes armed before it and no later one.
	{"unannotated-resources-after-try",
		`struct Rd { name: string }
function (r: Rd) shut(): void { print("close " + r.name); }
function open_rd(p: string): Option[Rd] { if (p == "") { return None; } return Some(Rd { name: p }); }
function both(p: string, q: string): Option[i32] {
    let r = open_rd(p)?;
    defer r.shut();
    let s = open_rd(q)?;
    defer s.shut();
    print("body");
    return Some(1);
}
function main(): i32 {
    match (both("a", "b")) { Some(v) => { print("some"); }, None => { print("none"); } }
    match (both("a", "")) { Some(v) => { print("some"); }, None => { print("none"); } }
    match (both("", "b")) { Some(v) => { print("some"); }, None => { print("none"); } }
    return 0;
}`, "body\nclose b\nclose a\nsome\nclose a\nnone\nnone\n", 0, false},
	// An explicit return ahead of the deferred binding replays nothing, and
	// an errdefer armed after a `?` fires only on the exits that follow it.
	{"return-before-deferred-binding",
		`struct Rd { name: string }
function (r: Rd) shut(): void { print("close " + r.name); }
function opened(p: string): Result[Rd, string] { if (p == "") { return Err("none"); } return Ok(Rd { name: p }); }
function g(p: string): Result[i32, string] {
    if (p == "early") { return Err("early"); }
    let r = opened(p)?;
    errdefer print("rollback " + r.name);
    defer r.shut();
    if (p == "fail") { return Err("failed"); }
    return Ok(1);
}
function main(): i32 {
    match (g("ok")) { Ok(v) => { print("ok"); }, Err(e) => { print(e); } }
    match (g("fail")) { Ok(v) => { print("ok"); }, Err(e) => { print(e); } }
    match (g("")) { Ok(v) => { print("ok"); }, Err(e) => { print(e); } }
    match (g("early")) { Ok(v) => { print("ok"); }, Err(e) => { print(e); } }
    return 0;
}`, "close ok\nok\nclose fail\nrollback fail\nfailed\nnone\nearly\n", 0, false},
}

// TestSelfHostTryDeferIRX86_64 cross-checks each case the interpreter can
// judge against it, then runs the self-hosted CLI's binary and compares
// stdout and exit code.
func TestSelfHostTryDeferIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range tryDeferIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.bumpMark {
				oracleOut, oracleCode := runInterp(t, tc.main+"\n")
				if oracleCode != tc.want || oracleOut != tc.wantOut {
					t.Fatalf("%s interpreter: out %q exit %d, want %q / %d", tc.name, oracleOut, oracleCode, tc.wantOut, tc.want)
				}
			}
			if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.main)); code != tc.want || out != tc.wantOut {
				t.Errorf("%s: out %q exit %d, want %q / %d (defers skipped at `?`)",
					tc.name, out, code, tc.wantOut, tc.want)
			}
		})
	}
}

// TestSelfHostTryDeferWasmIR runs the semantic core (fail-path firing,
// conditional registration, RC interplay) through the wasm backend.
func TestSelfHostTryDeferWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range tryDeferIRCases {
		if tc.name == "success-defer-at-return-only" || tc.name == "lifo-order-at-try-edge" || tc.name == "option-none-propagation" {
			continue // covered on x86-64; keep the wasm leg lean
		}
		t.Run(tc.name, func(t *testing.T) {
			if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", tc.main)); code != tc.want || out != tc.wantOut {
				t.Errorf("%s wasm: out %q exit %d, want %q / %d", tc.name, out, code, tc.wantOut, tc.want)
			}
		})
	}
}

// TestSelfHostTryDeferIRArm64 runs the core failure-path case plus the RC
// interplay case under qemu.
func TestSelfHostTryDeferIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range tryDeferIRCases {
		if tc.name != "result-fail-defer-and-errdefer" && tc.name != "rc-owned-array-reclaimed-with-defer" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.main)); code != tc.want || out != tc.wantOut {
				t.Errorf("%s arm64: out %q exit %d, want %q / %d", tc.name, out, code, tc.wantOut, tc.want)
			}
		})
	}
}
