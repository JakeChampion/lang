package e2e

import "testing"

// core/cmp's `impl Hash for string` and std/string's `hash_fnv32()` are ONE
// FNV-1a: the impl delegates rather than open-coding a second copy (#4387).
// The delegation crosses a signedness boundary — `hash_fnv32` is u32, `hash`
// is i32 — so it is a bit reinterpretation, sound because two's-complement
// multiply agrees with the unsigned one on the low 32 bits.
//
// Two properties, because either alone would pass on a broken change: the two
// spellings AGREE (a re-open-coded copy that drifted would fail), and they
// agree with FNV-1a's PUBLISHED values (both drifting together, e.g. a changed
// offset basis, would fail). The constants below are the 32-bit FNV-1a of each
// input, sign-reinterpreted into i32, computed independently of this tree.
//
// Not core/map's string hash: that one seeds the offset basis per process and
// adds an fmix32 avalanche, and is covered by map_string_hash_test.go.
const cmpStringHashProg = `import "core/cmp";
import "std/string";
import "std/i32";

function main(): i32 {
    // hash() == hash_fnv32() as i32, over inputs spanning empty / 1-byte /
    // multi-byte and two single-byte neighbours.
    var xs: string[] = ["", "a", "b", "hello", "hello world", "Fern"];
    var i: i32 = 0;
    while (i < xs.len()) {
        if (xs[i].hash() != (xs[i].hash_fnv32() as i32)) { return 1; }
        i = i + 1;
    }
    // Published FNV-1a 32-bit values, sign-reinterpreted.
    if ("".hash() != 0 - 2128831035) { return 2; }
    if ("a".hash() != 0 - 468965076) { return 3; }
    if ("b".hash() != 0 - 418632219) { return 4; }
    if ("hello".hash() != 1335831723) { return 5; }
    if ("hello world".hash() != 0 - 712294489) { return 6; }
    if ("Fern".hash() != 1329992820) { return 7; }
    // Distinct inputs hash distinctly here, and the hash is deterministic
    // (unseeded, unlike core/map's).
    if ("a".hash() == "b".hash()) { return 8; }
    if ("hello".hash() != "hello".hash()) { return 9; }
    return 42;
}
`

// TestNativeCmpStringHash runs the shipped modules on interp / x86-64 / wasm.
func TestNativeCmpStringHash(t *testing.T) {
	p := writeIterProg(t, cmpStringHashProg)
	if _, code := runFixtureInterp(t, p, ""); code != 42 {
		t.Errorf("cmp string hash interp = %d, want 42", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 42 {
		t.Errorf("cmp string hash x86-64 = %d, want 42", code)
	}
	if code := runWasm(t, cmpStringHashProg); code != 42 {
		t.Errorf("cmp string hash wasm = %d, want 42", code)
	}
}
