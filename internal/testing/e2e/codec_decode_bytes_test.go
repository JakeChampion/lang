package e2e

import "testing"

const codecDecodeBytesProgram = `
import "std/base64" as b64;
import "std/codec";
import "std/base32" as b32;
import "std/utf8" as utf8;

function main(): i32 {
    // "//79" is base64 for FF FE FD — three bytes that are not valid
    // UTF-8 anywhere, which is exactly why this cannot be a string.
    let raw: u8[] = __codec_bytes(b64.decode("//79"));
    if (raw.len() != 3) { return 1; }
    if (raw[0] as i32 != 255) { return 2; }
    if (raw[1] as i32 != 254) { return 3; }
    if (raw[2] as i32 != 253) { return 4; }
    match (utf8.from_bytes(raw)) {
        Some(s) => { return 5; },
        None => {}
    }

    // ASCII payloads still read back as text.
    match (utf8.from_bytes(__codec_bytes(b64.decode("TWFu")))) {
        Some(s) => { if (s != "Man") { return 6; } },
        None => { return 7; }
    }
    match (utf8.from_bytes(__codec_bytes(b32.decode("MZXW6YTBOI======")))) {
        Some(s) => { if (s != "foobar") { return 8; } },
        None => { return 9; }
    }

    // Empty decodes to an empty u8[], not to None.
    if (__codec_bytes(b64.decode("")).len() != 0) { return 10; }
    if (__codec_bytes(b32.decode("")).len() != 0) { return 11; }
    if (__codec_bytes(b64.decode_url("")).len() != 0) { return 12; }

    // "____" is the URL-safe form for FF FF FF.
    let u: u8[] = __codec_bytes(b64.decode_url("____"));
    if (u.len() != 3) { return 13; }
    if (u[0] as i32 != 255) { return 14; }

    // Every byte value round-trips byte-exactly through both codecs,
    // including the 128 that are not valid UTF-8 on their own.
    let i: i32 = 0;
    while (i < 256) {
        let one: u8[] = [i as u8];
        let b64back: u8[] = __codec_bytes(b64.decode(b64.encode(one)));
        if (b64back.len() != 1) { return 15; }
        if (b64back[0] as i32 != i) { return 16; }
        let b32back: u8[] = __codec_bytes(b32.decode(b32.encode(one)));
        if (b32back.len() != 1) { return 17; }
        if (b32back[0] as i32 != i) { return 18; }
        i = i + 1;
    }

    // Checked decoders return byte arrays for complete valid inputs.
    match (b64.decode("Zm9vYmFy")) {
        Ok(v) => { if (v.len() != 6) { return 19; } },
        Err(_) => { return 20; }
    }
    match (b64.decode("")) {
        Ok(v) => { if (v.len() != 0) { return 21; } },
        Err(_) => { return 22; }
    }
    match (b64.decode("Zm9v!")) {   // junk rejects
        Ok(v) => { return 23; },
        Err(_) => {}
    }
    match (b32.decode("MY======")) {
        Ok(v) => { if (v.len() != 1) { return 24; } },
        Err(_) => { return 25; }
    }
    match (b32.decode("MY======X")) {   // trailing junk rejects
        Ok(v) => { return 26; },
        Err(_) => {}
    }
    match (b64.decode_url("SGVsbG8+")) {  // non-url-safe '+' rejects
        Ok(v) => { return 27; },
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
