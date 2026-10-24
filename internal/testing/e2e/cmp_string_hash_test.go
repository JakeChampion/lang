package e2e

import "testing"

const cmpStringHashProg = `import "core/cmp";
import "std/string";
import "std/i32";

function main(): i32 {
    // hash() == hash_fnv32() as i32, over inputs spanning empty / 1-byte /
    // multi-byte and two single-byte neighbours.
    let xs: string[] = ["", "a", "b", "hello", "hello world", "Fern"];
    let i: i32 = 0;
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
