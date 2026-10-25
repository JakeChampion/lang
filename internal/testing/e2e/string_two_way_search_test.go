package e2e

import "testing"

const stringTwoWaySearchProg = `
import "std/string";
import "std/option";

// Naive leftmost-match reference.
function ref_find(s: string, needle: string): i32 {
    let n: i32 = s.len();
    let m: i32 = needle.len();
    if (m == 0) { return 0; }
    let i: i32 = 0;
    while (i + m <= n) {
        let k: i32 = 0;
        let ok: boolean = true;
        while (k < m) {
            if ((s[i + k] as i32) != (needle[k] as i32)) { ok = false; k = m; }
            else { k = k + 1; }
        }
        if (ok) { return i; }
        i = i + 1;
    }
    return 0 - 1;
}

// Naive NON-overlapping match positions -- the reference the find_all /
// count / split / replace family is defined against.
function ref_positions(s: string, sub: string): i32[] {
    let out: i32[] = [];
    let n: i32 = s.len();
    let m: i32 = sub.len();
    if (m == 0) { return out; }
    let i: i32 = 0;
    while (i + m <= n) {
        let k: i32 = 0;
        let ok: boolean = true;
        while (k < m) {
            if ((s[i + k] as i32) != (sub[k] as i32)) { ok = false; k = m; }
            else { k = k + 1; }
        }
        if (ok) { out = out.append(i); i = i + m; }
        else { i = i + 1; }
    }
    return out;
}

// The base-2 string of length ` + "`len`" + ` with index ` + "`code`" + `, over {a,b}.
function nth_string(code: i32, len: i32): string {
    let out: string = "";
    let c: i32 = code;
    let i: i32 = 0;
    while (i < len) {
        if (c % 2 == 0) { out = out + "a"; } else { out = out + "b"; }
        c = c / 2;
        i = i + 1;
    }
    return out;
}

function ipow2(e: i32): i32 {
    let r: i32 = 1;
    let i: i32 = 0;
    while (i < e) { r = r * 2; i = i + 1; }
    return r;
}

function main(): i32 {
    let hlen: i32 = 0;
    while (hlen <= 8) {
        let hi: i32 = 0;
        while (hi < ipow2(hlen)) {
            let hay: string = nth_string(hi, hlen);
            let nlen: i32 = 1;
            while (nlen <= 5) {
                let ni: i32 = 0;
                while (ni < ipow2(nlen)) {
                    let nee: string = nth_string(ni, nlen);
                    let first: i32 = ref_find(hay, nee);
                    let want: i32[] = ref_positions(hay, nee);

                    if (hay.index_of(nee).unwrap_or(-1) != first) { return 1; }
                    if (hay.contains(nee) != (first >= 0)) { return 2; }

                    let got: i32[] = hay.find_all(nee);
                    if (got.len() != want.len()) { return 3; }
                    let q: i32 = 0;
                    while (q < want.len()) {
                        if (got[q] != want[q]) { return 4; }
                        q = q + 1;
                    }
                    if (hay.count(nee) != want.len()) { return 5; }
                    if (hay.count(nee) != want.len()) { return 6; }

                    // split: piece count is matches+1, and rejoining on the
                    // separator must reproduce the input byte for byte.
                    let parts: string[] = hay.split(nee);
                    if (parts.len() != want.len() + 1) { return 7; }
                    let rebuilt: string = parts[0];
                    let p: i32 = 1;
                    while (p < parts.len()) {
                        rebuilt = rebuilt + nee + parts[p];
                        p = p + 1;
                    }
                    if (rebuilt != hay) { return 8; }

                    // replace: exact output length, and replacing back
                    // restores the original.
                    let rep: string = hay.replace(nee, "XY");
                    if (rep.len() != hay.len() + want.len() * (2 - nlen)) { return 9; }
                    if (rep.replace("XY", nee) != hay) { return 10; }
                    if (hay.replace_n(nee, "XY", 100) != rep) { return 11; }
                    if (hay.replace_n(nee, "XY", 100) != rep) { return 12; }
                    if (want.len() > 0) {
                        let one: string = hay.replace_n(nee, "XY", 1);
                        if (one != slice_unchecked(hay, 0, want[0]) + "XY" + slice_unchecked(hay, want[0] + nlen, hay.len())) { return 13; }
                    }

                    // splitn(2) is split_once's array form.
                    let s2: string[] = hay.splitn(nee, 2);
                    if (first >= 0) {
                        if (s2.len() != 2) { return 14; }
                        if (s2[0] != slice_unchecked(hay, 0, first)) { return 15; }
                        if (s2[1] != slice_unchecked(hay, first + nlen, hay.len())) { return 16; }
                    } else {
                        if (s2.len() != 1 || s2[0] != hay) { return 17; }
                    }

                    ni = ni + 1;
                }
                nlen = nlen + 1;
            }
            hi = hi + 1;
        }
        hlen = hlen + 1;
    }

    // Periodic / pathological shapes: the inputs that made the previous
    // naive scan quadratic, and that drive Two-Way's memory + period shift.
    let a30: string = "";
    let i: i32 = 0;
    while (i < 30) { a30 = a30 + "a"; i = i + 1; }
    if (a30.index_of(a30 + "b").is_some()) { return 20; }
    if ((a30 + "b").index_of(a30 + "b").unwrap_or(-1) != 0) { return 21; }
    if ((a30 + a30 + "b").index_of(a30 + "b").unwrap_or(-1) != 30) { return 22; }
    if ("abababababab".index_of("ababab").unwrap_or(-1) != 0) { return 23; }
    if ("xabababababab".index_of("bababa").unwrap_or(-1) != 2) { return 24; }
    if ("mississippi".count("ss") != 2) { return 25; }
    if ("mississippi".replace("ss", "SS") != "miSSiSSippi") { return 26; }

    // Empty-needle contracts, unchanged by the Two-Way rewrite.
    if ("abc".index_of("").unwrap_or(-1) != 0) { return 30; }
    if (!"abc".contains("")) { return 31; }
    if ("abc".count("") != 0) { return 32; }
    if ("abc".find_all("").len() != 0) { return 33; }
    if ("abc".replace("", "X") != "abc") { return 34; }
    if ("".index_of("a").is_some()) { return 35; }
    if ("abc".last_index_of("").unwrap_or(-1) != 3) { return 36; }

    return 42;
}
`

func TestStringTwoWaySearchInterp(t *testing.T) {
	if got := runInterpExit(t, stringTwoWaySearchProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringTwoWaySearchX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringTwoWaySearchProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringTwoWaySearchWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringTwoWaySearchProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringTwoWaySearchArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringTwoWaySearchProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
