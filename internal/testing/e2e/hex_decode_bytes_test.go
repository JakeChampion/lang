package e2e

import "testing"

const hexDecodeBytesProgram = `
import "std/hex" as hex;
import "std/codec";
import "std/utf8" as utf8;

function main(): i32 {
    // The bytes come back exactly, including ones that are not text.
    let b: u8[] = __codec_bytes(hex.decode("ff"));
    if (b.len() != 1) { return 1; }
    if (b[0] as i32 != 255) { return 2; }
    // ...and they really are invalid UTF-8, which is the whole reason
    // this is not a string: the checked constructor rejects them.
    match (utf8.from_bytes(b)) {
        Some(s) => { return 3; },
        None => {}
    }

    // ASCII payloads decode to bytes that DO read back as text.
    let hi: u8[] = __codec_bytes(hex.decode("6869"));
    match (utf8.from_bytes(hi)) {
        Some(s) => { if (s != "hi") { return 4; } },
        None => { return 5; }
    }

    // Empty input succeeds. Malformed input returns a typed error.
    if (__codec_bytes(hex.decode("")).len() != 0) { return 6; }
    match (hex.decode("414")) { Err(e) => { if (e.offset != 3 || e.kind != codec.InvalidLength) { return 7; } }, Ok(_) => { return 7; } }
    match (hex.decode("41xx")) { Err(e) => { if (e.offset != 2 || e.kind != codec.InvalidByte(120 as u8)) { return 8; } }, Ok(_) => { return 8; } }

    // Every byte value round-trips through encode/decode byte-exactly,
    // including the 128 that are not valid UTF-8 on their own.
    let i: i32 = 0;
    while (i < 256) {
        let one: u8[] = [i as u8];
        let enc: string = hex.encode(one);
        if (enc.len() != 2) { return 9; }
        let back: u8[] = __codec_bytes(hex.decode(enc));
        if (back.len() != 1) { return 10; }
        if (back[0] as i32 != i) { return 11; }
        i = i + 1;
    }

    // Checked decoding returns owned bytes on success.
    match (hex.decode("48656c6c6f")) {
        Ok(v) => { if (v.len() != 5) { return 12; } },
        Err(_) => { return 13; }
    }
    match (hex.decode("")) {
        Ok(v) => { if (v.len() != 0) { return 14; } },
        Err(_) => { return 15; }
    }
    match (hex.decode("414")) {   // odd length rejects
        Ok(v) => { return 16; },
        Err(_) => {}
    }
    match (hex.decode("zz")) {    // non-hex rejects
        Ok(v) => { return 17; },
        Err(_) => {}
    }
    return 0;
}

// Decode a valid fixture. Report a malformed fixture before ending the test.
function __codec_bytes(value: Result[u8[], codec.DecodeError]): u8[] {
  match (value) {
    Ok(bytes) => { return bytes; },
    Err(problem) => { eprint(problem.message()); exit(98); return []; }
  }
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
