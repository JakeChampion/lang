package e2e

// core/int's integer→string conversion.
//
// It was one divide AND one modulo per decimal digit, written backwards into
// an over-sized scratch buffer, followed by a full copy pass into a
// right-sized one — so a ten-digit number cost ten divisions, ten modulos, and
// two allocations.
//
// It now consumes TWO digits per division against a 200-byte digit-pair table,
// and `__int_u32_digits` gives the width before the loop starts so the result
// is written straight into an exactly-sized buffer with nothing to re-pack.
// Five divisions and one allocation for that same ten-digit number.
//
// Measured on 4M i32 conversions, x86-64: 0.721s → 0.463s.
//
// The risk this change carries is entirely in the ODD/EVEN digit boundary. A
// two-at-a-time loop leaves either one or two digits at the end, and the
// leftover has to be emitted at the right width and the right offset — an
// off-by-one there produces a leading zero, a dropped digit, or a stray byte
// only for inputs of one particular parity. So the coverage is exhaustive over
// small values and dense around every power of ten, differentialled against a
// naive one-digit-at-a-time reference in the same program rather than against
// a table of expected strings.

import "testing"

const intToStringPairsProg = `
import "std/i32";
import "std/i64";
import "std/u32";
import "std/u64";
import "std/string";

// One digit at a time, in i64 so INT_MIN's magnitude is exact -- the shape
// this replaced, as the oracle.
function ref_itos(n: i32): string {
    if (n == 0) { return "0"; }
    var neg: boolean = false;
    var m: i64 = n as i64;
    if (m < 0) { neg = true; m = 0 - m; }
    var out: string = "";
    while (m > (0 as i64)) {
        var d: i32 = (m % (10 as i64)) as i32;
        out = ((d + 48) as u8).to_ascii_string() + out;
        m = m / (10 as i64);
    }
    if (neg) { out = "-" + out; }
    return out;
}

function ref_itos64(m0: i64): string {
    if (m0 == (0 as i64)) { return "0"; }
    var neg: boolean = false;
    var m: i64 = m0;
    if (m < (0 as i64)) { neg = true; m = 0 - m; }
    var out: string = "";
    while (m > (0 as i64)) {
        var d: i32 = (m % (10 as i64)) as i32;
        out = ((d + 48) as u8).to_ascii_string() + out;
        m = m / (10 as i64);
    }
    if (neg) { out = "-" + out; }
    return out;
}

function main(): i32 {
    // Exhaustive 0..20000, both signs: covers every digit count 1..5 and both
    // parities at each, which is where a two-at-a-time loop goes wrong.
    var i: i32 = 0;
    while (i <= 20000) {
        if (i.to_string() != ref_itos(i)) { return 1; }
        if ((0 - i).to_string() != ref_itos(0 - i)) { return 2; }
        i = i + 1;
    }

    // Dense around every power of ten -- the digit-count transitions, where
    // the up-front width and the loop's leftover must agree.
    var p: i64 = 1 as i64;
    while (p < (2000000000 as i64)) {
        var v: i32 = p as i32;
        if (v.to_string() != ref_itos(v)) { return 3; }
        if ((v - 1).to_string() != ref_itos(v - 1)) { return 4; }
        if ((v + 1).to_string() != ref_itos(v + 1)) { return 5; }
        if ((0 - v).to_string() != ref_itos(0 - v)) { return 6; }
        if ((0 - v - 1).to_string() != ref_itos(0 - v - 1)) { return 7; }
        p = p * (10 as i64);
    }

    // The i32 bounds. INT_MIN is the one value whose magnitude does not fit
    // in i32, so it takes the special-cased path in std/i32.
    if ((2147483647).to_string() != "2147483647") { return 8; }
    if ((0 - 2147483647 - 1).to_string() != "-2147483648") { return 9; }
    if ((0).to_string() != "0") { return 10; }

    // The 64-bit sibling shares the pair table but counts its width by
    // repeated division rather than a comparison ladder.
    if ((0 as i64).to_string() != "0") { return 11; }
    if ((9 as i64).to_string() != "9") { return 12; }
    if ((10 as i64).to_string() != "10") { return 13; }
    if ((99 as i64).to_string() != "99") { return 14; }
    if ((100 as i64).to_string() != "100") { return 15; }
    if ((9223372036854775807 as i64).to_string() != "9223372036854775807") { return 16; }
    if ((0 - 9223372036854775807 as i64).to_string() != "-9223372036854775807") { return 17; }
    if ((1234567890123 as i64).to_string() != "1234567890123") { return 18; }

    // i64 exhaustive over the small range + both parities.
    var q: i64 = 0 as i64;
    while (q <= (3000 as i64)) {
        if (q.to_string() != ref_itos64(q)) { return 19; }
        if ((0 - q).to_string() != ref_itos64(0 - q)) { return 20; }
        q = q + 1;
    }
    // And dense around each 64-bit power of ten.
    var e: i64 = 1 as i64;
    while (e <= (1000000000000000000 as i64)) {
        if (e.to_string() != ref_itos64(e)) { return 21; }
        if ((e - 1).to_string() != ref_itos64(e - 1)) { return 22; }
        if ((e + 1).to_string() != ref_itos64(e + 1)) { return 23; }
        e = e * (10 as i64);
    }

    // Unsigned, where the top value exceeds the signed range entirely.
    if ((0 as u32).to_string() != "0") { return 24; }
    if ((4294967295 as u32).to_string() != "4294967295") { return 25; }
    if ((0 as u64).to_string() != "0") { return 26; }
    if ((18446744073709551615 as u64).to_string() != "18446744073709551615") { return 27; }

    return 42;
}
`

func TestIntToStringPairsInterp(t *testing.T) {
	if got := runInterpExit(t, intToStringPairsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntToStringPairsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intToStringPairsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntToStringPairsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intToStringPairsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntToStringPairsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intToStringPairsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
