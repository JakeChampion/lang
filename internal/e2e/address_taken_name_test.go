package e2e

import "testing"

// A param or local that shares a function's name is not a reference to the
// function (#10671): `shift`'s param and `pick`'s then-branch local both
// share `label`'s name, the else branch reads `label` as a value, and every
// read resolves to the binding it names. Before the rename the else branch
// read the then branch's slot as a function value and crashed.
const addressTakenNameSrc = `import "std/i32";
function label(n: i32): Option[string] {
    if (n < 0) { return None; }
    return Some("n" + n.to_string());
}
function shift(label: string): string { return label + "!"; }
function pick(c: boolean, n: i32): string {
    if (c) {
        var label: string = "local";
        return label;
    } else {
        var f: (i32) => Option[string] = label;
        match (f(n)) {
            Some(s) => { return s; },
            None => { return "none"; }
        }
    }
    return "";
}
function main(): i32 {
    var out: string = "";
    var i: i32 = 0;
    while (i < 50) {
        match (label(i)) {
            Some(s) => { out = shift(s); },
            None => { return 1; }
        }
        i = i + 1;
    }
    if (out != "n49!") { return 2; }
    if (pick(true, 7) != "local") { return 3; }
    if (pick(false, 7) != "n7") { return 4; }
    if (pick(false, -1) != "none") { return 5; }
    return 0;
}
`

func TestLeakCheckAddressTakenNameX86_64(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, addressTakenNameSrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d", code, allocs, frees, live)
	}
}

func TestLeakCheckAddressTakenNameArm64(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, addressTakenNameSrc)
	allocs, frees, live := leakSummaryIn(t, stderr)
	if code != 0 || allocs != frees || live != 0 {
		t.Fatalf("code=%d allocs=%d frees=%d live_bytes=%d", code, allocs, frees, live)
	}
}
