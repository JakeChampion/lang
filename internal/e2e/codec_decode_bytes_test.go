package e2e

import "testing"

// codecDecodeBytesProgram pins that std/base64 and std/base32's decoders
// yield `u8[]` rather than `string` (#5730, the D9 follow-up), the same
// contract std/hex got in the first slice.
//
// The point is not the type per se: `base64_decode` of a PNG produces
// arbitrary bytes, and a `string` is supposed to mean well-formed UTF-8.
// As long as these came back as `string`, the D9 invariant was false by
// construction for every caller — and this is the *normal* use of these
// APIs, not an edge case.
//
// Exits 0 on success, a distinct code per failed step.
const codecDecodeBytesProgram = `
import "std/base64" as b64;
import "std/base32" as b32;
import "std/utf8" as utf8;

function main(): i32 {
    // "//79" is base64 for FF FE FD — three bytes that are not valid
    // UTF-8 anywhere, which is exactly why this cannot be a string.
    var raw: u8[] = b64.base64_decode("//79");
    if (raw.len() != 3) { return 1; }
    if (raw[0] as i32 != 255) { return 2; }
    if (raw[1] as i32 != 254) { return 3; }
    if (raw[2] as i32 != 253) { return 4; }
    match (utf8.from_bytes(raw)) {
        Some(s) => { return 5; },
        None => {}
    }

    // ASCII payloads still read back as text.
    match (utf8.from_bytes(b64.base64_decode("TWFu"))) {
        Some(s) => { if (s != "Man") { return 6; } },
        None => { return 7; }
    }
    match (utf8.from_bytes(b32.base32_decode("MZXW6YTBOI======"))) {
        Some(s) => { if (s != "foobar") { return 8; } },
        None => { return 9; }
    }

    // Empty decodes to an empty u8[], not to None.
    if (b64.base64_decode("").len() != 0) { return 10; }
    if (b32.base32_decode("").len() != 0) { return 11; }
    if (b64.base64url_decode("").len() != 0) { return 12; }

    // The url-safe decoder delegates to the standard one, so it moved
    // with it: "____" is url-safe for FF FF FF.
    var u: u8[] = b64.base64url_decode("____");
    if (u.len() != 3) { return 13; }
    if (u[0] as i32 != 255) { return 14; }

    // Every byte value round-trips byte-exactly through both codecs,
    // including the 128 that are not valid UTF-8 on their own.
    var i: i32 = 0;
    while (i < 256) {
        var one: u8[] = [i as u8];
        var b64back: u8[] = b64.base64_decode(b64.base64_encode(one));
        if (b64back.len() != 1) { return 15; }
        if (b64back[0] as i32 != i) { return 16; }
        var b32back: u8[] = b32.base32_decode(b32.base32_encode(one));
        if (b32back.len() != 1) { return 17; }
        if (b32back[0] as i32 != i) { return 18; }
        i = i + 1;
    }

    // The strict decoders are Option[u8[]] and agree on the same inputs.
    match (b64.base64_decode_strict("Zm9vYmFy")) {
        Some(v) => { if (v.len() != 6) { return 19; } },
        None => { return 20; }
    }
    match (b64.base64_decode_strict("")) {
        Some(v) => { if (v.len() != 0) { return 21; } },
        None => { return 22; }
    }
    match (b64.base64_decode_strict("Zm9v!")) {   // junk rejects
        Some(v) => { return 23; },
        None => {}
    }
    match (b32.base32_decode_strict("MY======")) {
        Some(v) => { if (v.len() != 1) { return 24; } },
        None => { return 25; }
    }
    match (b32.base32_decode_strict("MY======X")) {   // trailing junk rejects
        Some(v) => { return 26; },
        None => {}
    }
    match (b64.base64url_decode_strict("SGVsbG8+")) {  // non-url-safe '+' rejects
        Some(v) => { return 27; },
        None => {}
    }
    return 0;
}
`

func TestCodecDecodeBytesInterp(t *testing.T) {
	if got := runInterpExit(t, codecDecodeBytesProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestCodecDecodeBytesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, codecDecodeBytesProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestCodecDecodeBytesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, codecDecodeBytesProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestCodecDecodeBytesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, codecDecodeBytesProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
