package e2e

import "testing"

// An arm that RETURNS the payload of a fresh heap-box scrutinee (#10673): the
// return takes the transfer inc, so the caller holds its own reference and
// the box is the arm's to release on the way out. Before, the returned
// binding refused the reclaim and the box leaked per call. `lab` is kept
// off the pair-form ABI by returning through a local, and `f` reaches it
// through a function value, the other route to a heap box.
const matchReturnPayloadSrc = `import "std/i32";
struct Box { s: string }
function lab(n: i32): Option[string] {
    let r: Option[string] = None;
    if (n >= 0) { r = Some("n" + n.to_string()); }
    return r;
}
function boxed(n: i32): Option[Box] {
    if (n < 0) { return None; }
    return Some(Box { s: "b" + n.to_string() });
}
function pick(n: i32): string {
    match (lab(n)) {
        Some(s) => { return s; },
        None => { return "none"; }
    }
    return "";
}
function pick_boxed(n: i32): Box {
    let f: (i32) => Option[Box] = boxed;
    match (f(n)) {
        Some(b) => { return b; },
        None => { return Box { s: "none" }; }
    }
    return Box { s: "" };
}
function main(): i32 {
    let i: i32 = 0;
    let n: i32 = 0;
    while (i < 20) {
        n = n + pick(i).len() + pick_boxed(i).s.len();
        i = i + 1;
    }
    if (pick(-1) != "none" || pick_boxed(-1).s != "none") { return 1; }
    return n - 100;
}
`

func TestLeakCheckMatchReturnPayloadX86_64(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, matchReturnPayloadSrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: the box whose payload the arm returns is not released", code, allocs, frees, live)
	}
}

func TestLeakCheckMatchReturnPayloadArm64(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, matchReturnPayloadSrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: the box whose payload the arm returns is not released", code, allocs, frees, live)
	}
}
