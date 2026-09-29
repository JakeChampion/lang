package e2e

import "testing"

// A match payload read by a binary operator in the arm (#10678): a string
// concatenation answers a fresh string and a comparison a boolean, so the
// arm's release of the payload (pair-form) or of the box (heap-box) is
// safe, and before the excuse both leaked one per call. `target` is
// std/http's request-target shape, the decoded path joined to its query
// on the way out of the arm.
const matchPayloadBinarySrc = `import "std/i32";
function pair(n: i32): Result[string, i32] {
    if (n < 0) { return Err(400); }
    return Ok("p" + n.to_string());
}
function boxed(n: i32): Option[string] {
    var r: Option[string] = None;
    if (n >= 0) { r = Some("b" + n.to_string()); }
    return r;
}
function target(n: i32, q: boolean): Result[string, i32] {
    match (pair(n)) {
        Ok(p) => {
            if (!q) { return Ok(p); }
            return Ok(p + "?x");
        },
        Err(status) => { return Err(status); }
    }
    return Err(400);
}
function is_three(n: i32): boolean {
    match (boxed(n)) {
        Some(b) => { return b == "b3"; },
        None => { return false; }
    }
    return false;
}
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 40) {
        match (target(i, i % 2 == 0)) { Ok(s) => { total = total + s.len(); }, Err(_) => { return 1; } }
        if (is_three(i)) { total = total + 100; }
        i = i + 1;
    }
    match (target(-1, true)) { Ok(_) => { return 2; }, Err(status) => { if (status != 400) { return 3; } } }
    return total - 250;
}
`

func TestLeakCheckMatchPayloadBinaryX86_64(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, matchPayloadBinarySrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: a match payload read by a binary operator is not released", code, allocs, frees, live)
	}
}

func TestLeakCheckMatchPayloadBinaryArm64(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, matchPayloadBinarySrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: a match payload read by a binary operator is not released", code, allocs, frees, live)
	}
}
