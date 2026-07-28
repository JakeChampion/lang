package e2e

import "testing"

// hexDecodeBytesProgram pins that `std/hex`'s decoders yield `u8[]`
// rather than `string` (#5730, the D9 follow-up).
//
// The point is not the type per se: decoding arbitrary hex produces
// arbitrary bytes, and a `string` is supposed to mean well-formed
// UTF-8. `hex_decode("ff")` is a single 0xFF byte, which is not valid
// UTF-8 in any position — so as long as that came back as a `string`,
// the D9 invariant was false by construction for every caller.
//
// Exits 0 on success, a distinct code per failed step.
const hexDecodeBytesProgram = `
import "std/hex" as hex;
import "std/utf8" as utf8;

function main(): i32 {
    // The bytes come back exactly, including ones that are not text.
    var b: u8[] = hex.hex_decode("ff");
    if (b.len() != 1) { return 1; }
    if (b[0] as i32 != 255) { return 2; }
    // ...and they really are invalid UTF-8, which is the whole reason
    // this is not a string: the checked constructor rejects them.
    match (utf8.from_bytes(b)) {
        Some(s) => { return 3; },
        None => {}
    }

    // ASCII payloads decode to bytes that DO read back as text.
    var hi: u8[] = hex.hex_decode("6869");
    match (utf8.from_bytes(hi)) {
        Some(s) => { if (s != "hi") { return 4; } },
        None => { return 5; }
    }

    // Empty and the lenient terminations still behave, as u8[].
    if (hex.hex_decode("").len() != 0) { return 6; }
    if (hex.hex_decode("414").len() != 1) { return 7; }    // odd tail drops
    if (hex.hex_decode("41xx").len() != 1) { return 8; }   // stops at non-hex

    // Every byte value round-trips through encode/decode byte-exactly,
    // including the 128 that are not valid UTF-8 on their own.
    var i: i32 = 0;
    while (i < 256) {
        var one: u8[] = [i as u8];
        var enc: string = hex.hex_encode(string_from_bytes_unchecked(one));
        if (enc.len() != 2) { return 9; }
        var back: u8[] = hex.hex_decode(enc);
        if (back.len() != 1) { return 10; }
        if (back[0] as i32 != i) { return 11; }
        i = i + 1;
    }

    // The strict decoder is Option[u8[]] and agrees on the same inputs.
    match (hex.hex_decode_strict("48656c6c6f")) {
        Some(v) => { if (v.len() != 5) { return 12; } },
        None => { return 13; }
    }
    match (hex.hex_decode_strict("")) {
        Some(v) => { if (v.len() != 0) { return 14; } },
        None => { return 15; }
    }
    match (hex.hex_decode_strict("414")) {   // odd length rejects
        Some(v) => { return 16; },
        None => {}
    }
    match (hex.hex_decode_strict("zz")) {    // non-hex rejects
        Some(v) => { return 17; },
        None => {}
    }
    return 0;
}
`

func TestHexDecodeBytesInterp(t *testing.T) {
	if got := runInterpExit(t, hexDecodeBytesProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestHexDecodeBytesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, hexDecodeBytesProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestHexDecodeBytesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, hexDecodeBytesProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestHexDecodeBytesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, hexDecodeBytesProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
