package e2e

import "testing"

const matchPayloadInLiteralSrc = `import "std/i32";
struct Row { at: i32, s: string }
function pair(n: i32): Option[string] {
    if (n < 0) { return None; }
    return Some("p" + n.to_string());
}
function boxed(n: i32): Result[string, i32] {
    let r: Result[string, i32] = Err(400);
    if (n >= 0) { r = Ok("b" + n.to_string()); }
    return r;
}
function line(n: i32): (i32, string) {
    match (boxed(n)) {
        Ok(p) => { return (0, p); },
        Err(status) => { return (status, ""); }
    }
    return (400, "");
}
function row(n: i32): Row {
    match (pair(n)) {
        Some(p) => { return Row { at: n, s: p }; },
        None => { return Row { at: -1, s: "" }; }
    }
    return Row { at: -1, s: "" };
}
function cells(n: i32): string[] {
    match (pair(n)) {
        Some(p) => { return [p, "x"]; },
        None => { return []; }
    }
    return [];
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 40) {
        total = total + line(i).1.len() + row(i).s.len() + cells(i).len();
        i = i + 1;
    }
    if (line(-1).0 != 400 || row(-1).at != -1 || cells(-1).len() != 0) { return 1; }
    return total - 300;
}
`

func TestLeakCheckMatchPayloadInLiteralX86_64(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, matchPayloadInLiteralSrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: a match payload stored into a literal is not released", code, allocs, frees, live)
	}
}

func TestLeakCheckMatchPayloadInLiteralArm64(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, matchPayloadInLiteralSrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d: a match payload stored into a literal is not released", code, allocs, frees, live)
	}
}
