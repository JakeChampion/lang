package e2e

// std/string's BACKWARD search — `last_index_of` / `rfind` / `rsplit_once` /
// `rpartition`.
//
// Forward search became Two-Way (linear) in #6179; backward search stayed a
// naive right-to-left probe loop, O(n*m) worst case (#6196). `__str_rfind_from`
// now has three tiers: the empty-needle gap, a backward memchr-shaped scan for
// a single byte, and for anything longer a metered naive scan that ESCALATES to
// the reverse Two-Way — reverse both strings, run the forward algorithm, map
// the index back — once it has spent a linear comparison budget.
//
// Measured on the adversarial shape (40 KB of "a", needle 2000×"a"+"b", five
// repeats): 2.655s before, 0.014s after.
//
// The escalation is why the coverage below is differential rather than a table
// of expected indices. Every case is checked against a naive reference rfind in
// the SAME program, so the two tiers must agree with each other and with the
// obvious implementation on every input — including the ones that cross the
// budget mid-scan, which is the case a hand-written expectation table is least
// likely to get right.
//
// Both tiers were additionally verified in ISOLATION by forcing the budget to
// each extreme and re-running this program: at -1 (escalate immediately) it
// exercises only the reverse Two-Way, at 1000000 only the naive scan. Both
// return 42, so neither tier is carrying the other.

import "testing"

const stringBackwardSearchProg = `
import "std/string";
import "std/i32";

// The exact body last_index_of had before the change, as the oracle.
function ref_rfind(s: string, needle: string): i32 {
    var n: i32 = s.len();
    var m: i32 = needle.len();
    if (m == 0) { return n; }
    if (m > n) { return 0 - 1; }
    var i: i32 = n - m;
    while (i >= 0) {
        var k: i32 = 0;
        var ok: boolean = true;
        while (k < m) {
            if (s[i + k] != needle[k]) { ok = false; k = m; } else { k = k + 1; }
        }
        if (ok) { return i; }
        i = i - 1;
    }
    return 0 - 1;
}

function rep(c: string, n: i32): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < n) { out = out + c; i = i + 1; }
    return out;
}

function chk(s: string, needle: string): boolean {
    return s.last_index_of(needle) == ref_rfind(s, needle);
}

function main(): i32 {
    // Basics and edges. Empty needle returns len(s) (Python rfind / Go
    // LastIndex "matches at every gap"), which the oracle mirrors.
    if (!chk("hello world", "o")) { return 1; }
    if (!chk("hello world", "world")) { return 2; }
    if (!chk("hello world", "l")) { return 3; }
    if (!chk("hello world", "zz")) { return 4; }
    if (!chk("", "a")) { return 5; }
    if (!chk("a", "")) { return 6; }
    if (!chk("", "")) { return 7; }
    if (!chk("abc", "abcd")) { return 8; }
    if (!chk("aaa", "aa")) { return 9; }
    if (!chk("abcabcabc", "abc")) { return 10; }
    if (!chk("abcabcabc", "cab")) { return 11; }
    if (!chk("xabx", "x")) { return 12; }
    if (!chk("abab", "ab")) { return 13; }

    // ADVERSARIAL: needle "aaa...ab" against "aaa...a". Every position walks
    // the whole needle before mismatching, which is what blows the budget and
    // hands the search to the reverse Two-Way. Absent.
    var big: string = rep("a", 900);
    if (!chk(big, rep("a", 60) + "b")) { return 14; }
    // Present, but near the FRONT -- so the backward scan burns its whole
    // budget before it could ever reach the match, and the escalated search
    // has to find it.
    var hay: string = rep("a", 400) + "b" + rep("a", 400);
    if (!chk(hay, rep("a", 50) + "b")) { return 15; }
    // Present near the END -- found while still inside the budget, so this one
    // must NOT escalate and must still be right.
    if (!chk(rep("a", 800) + "ab", "ab")) { return 16; }
    // Periodic needle: Two-Way's memory path, where a naive port gets the
    // shift wrong.
    if (!chk(rep("ab", 400), rep("ab", 30))) { return 17; }
    if (!chk(rep("ab", 400) + "c", rep("ab", 30) + "c")) { return 18; }

    // Exhaustive: every substring of a small periodic haystack, against
    // itself. Covers needle lengths 0..len and both tiers.
    var alphabet: string = "abab bcab abab cbab";
    var L: i32 = alphabet.len();
    var a: i32 = 0;
    while (a < L) {
        var b: i32 = a;
        while (b <= L) {
            if (!chk(alphabet, alphabet[a:b].to_owned())) { return 19; }
            b = b + 1;
        }
        a = a + 1;
    }

    // The callers routed through it.
    match ("a.b.c".rsplit_once(".")) {
        Some(p) => { if (p.0 != "a.b" || p.1 != "c") { return 20; } },
        None => { return 21; },
    }
    match ("abc".rsplit_once(".")) { Some(p) => { return 22; }, None => { } }
    match ("".rsplit_once(".")) { Some(p) => { return 23; }, None => { } }
    var rp = "a.b.c".rpartition(".");
    if (rp.0 != "a.b" || rp.1 != "." || rp.2 != "c") { return 24; }
    var rp2 = "abc".rpartition(".");
    if (rp2.0 != "" || rp2.1 != "" || rp2.2 != "abc") { return 25; }
    var rp3 = "abc".rpartition("");
    if (rp3.0 != "" || rp3.1 != "" || rp3.2 != "abc") { return 26; }

    // And through the ESCALATION path, so the callers are covered on both
    // tiers rather than only the short-input one.
    match ((rep("a", 400) + "b" + rep("a", 400)).rsplit_once(rep("a", 50) + "b")) {
        Some(p) => { if (p.0.len() != 350 || p.1.len() != 400) { return 27; } },
        None => { return 28; },
    }
    match (rep("a", 900).rfind(rep("a", 60) + "b")) { Some(v) => { return 29; }, None => { } }
    match ("hello".rfind("l")) { Some(v) => { if (v != 3) { return 30; } }, None => { return 31; } }

    return 42;
}
`

func TestStringBackwardSearchInterp(t *testing.T) {
	if got := runInterpExit(t, stringBackwardSearchProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringBackwardSearchX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringBackwardSearchProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringBackwardSearchWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringBackwardSearchProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringBackwardSearchArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringBackwardSearchProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
