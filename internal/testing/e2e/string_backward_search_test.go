package e2e

import "testing"

const stringBackwardSearchProg = `
import "std/string";
import "std/i32";

// The exact body last_index_of had before the change, as the oracle.
function ref_rfind(s: string, needle: string): i32 {
    let n: i32 = s.len();
    let m: i32 = needle.len();
    if (m == 0) { return n; }
    if (m > n) { return 0 - 1; }
    let i: i32 = n - m;
    while (i >= 0) {
        let k: i32 = 0;
        let ok: boolean = true;
        while (k < m) {
            if (s[i + k] != needle[k]) { ok = false; k = m; } else { k = k + 1; }
        }
        if (ok) { return i; }
        i = i - 1;
    }
    return 0 - 1;
}

function rep(c: string, n: i32): string {
    let out: string = "";
    let i: i32 = 0;
    while (i < n) { out = out + c; i = i + 1; }
    return out;
}

function chk(s: string, needle: string): boolean {
    match (s.last_index_of(needle)) {
        Some(i) => { return i == ref_rfind(s, needle); },
        None => { return ref_rfind(s, needle) == 0 - 1; }
    }
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
    // the whole needle before mismatching, which is what exhausts the budget and
    // hands the search to the reverse Two-Way. Absent.
    let big: string = rep("a", 900);
    if (!chk(big, rep("a", 60) + "b")) { return 14; }
    // Present, but near the FRONT -- so the backward scan spends its whole
    // budget before it could ever reach the match, and the escalated search
    // has to find it.
    let hay: string = rep("a", 400) + "b" + rep("a", 400);
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
    let alphabet: string = "abab bcab abab cbab";
    let L: i32 = alphabet.len();
    let a: i32 = 0;
    while (a < L) {
        let b: i32 = a;
        while (b <= L) {
            if (!chk(alphabet, slice_unchecked(alphabet, a, b).to_owned())) { return 19; }
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
    let rp = "a.b.c".rpartition(".");
    if (rp.0 != "a.b" || rp.1 != "." || rp.2 != "c") { return 24; }
    let rp2 = "abc".rpartition(".");
    if (rp2.0 != "" || rp2.1 != "" || rp2.2 != "abc") { return 25; }
    let rp3 = "abc".rpartition("");
    if (rp3.0 != "" || rp3.1 != "" || rp3.2 != "abc") { return 26; }

    // And through the ESCALATION path, so the callers are covered on both
    // tiers rather than only the short-input one.
    match ((rep("a", 400) + "b" + rep("a", 400)).rsplit_once(rep("a", 50) + "b")) {
        Some(p) => { if (p.0.len() != 350 || p.1.len() != 400) { return 27; } },
        None => { return 28; },
    }
    match (rep("a", 900).last_index_of(rep("a", 60) + "b")) { Some(v) => { return 29; }, None => { } }
    match ("hello".last_index_of("l")) { Some(v) => { if (v != 3) { return 30; } }, None => { return 31; } }

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
