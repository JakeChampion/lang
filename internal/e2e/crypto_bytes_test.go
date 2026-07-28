package e2e

import "testing"

// cryptoBytesProgram pins that `std/crypto` carries bytes as `u8[]`
// rather than `string` (#5730, the D9 follow-up), and that the encoders
// take `u8[]` on the input side so a digest pipes straight into one.
//
// The point is not the types per se. A SHA-256 digest is 32 arbitrary
// bytes and is essentially never valid UTF-8 — so as long as
// `sha256_bytes` came back as a `string`, the D9 invariant ("a `string`
// is well-formed UTF-8") was false by construction for every caller of
// this module. The `utf8.from_bytes` rejection below is the proof: those
// bytes really cannot be a `string`.
//
// It also pins the shape of the migration: the `*_hex` variants still
// return a `string` (hex output genuinely is text), the message to hash
// and the password to stretch are still `string`, and the whole
// key / salt / IKM / info side is `u8[]`.
//
// Exits 0 on success, a distinct code per failed step.
const cryptoBytesProgram = `
import "std/crypto" as crypto;
import "std/hex" as hex;
import "std/base32" as b32;
import "std/utf8" as utf8;
import "std/string";

function main(): i32 {
    // A digest is u8[], 32 bytes wide.
    var d: u8[] = crypto.sha256_bytes("abc");
    if (d.len() != 32) { return 1; }
    if (d[0] as i32 != 186) { return 2; }   // 0xba — SHA-256("abc") starts ba78…

    // ...and it is NOT valid UTF-8, which is the whole reason it is not
    // a string: the checked constructor rejects it.
    match (utf8.from_bytes(d)) {
        Some(s) => { return 3; },
        None => {}
    }

    // The digest pipes straight into an encoder — no intervening
    // unchecked construction — and agrees with the _hex variant, which
    // does still return text.
    if (hex.hex_encode(d) != crypto.sha256_hex("abc")) { return 4; }
    if (hex.hex_encode(d) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") { return 5; }

    // An HMAC key is u8[]; the message stays text.
    var key: u8[] = "key".bytes();
    var mac: u8[] = crypto.hmac_sha256_bytes(key, "The quick brown fox jumps over the lazy dog");
    if (mac.len() != 32) { return 6; }
    if (hex.hex_encode(mac) != "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8") { return 7; }

    // consteq compares u8[], so hmac_verify takes the digest directly.
    if (!crypto.consteq(mac, mac)) { return 8; }
    if (crypto.consteq(mac, d)) { return 9; }
    if (!crypto.hmac_verify(key, "The quick brown fox jumps over the lazy dog", mac)) { return 10; }
    if (crypto.hmac_verify("wrongkey".bytes(), "The quick brown fox jumps over the lazy dog", mac)) { return 11; }

    // PBKDF2: password is text, salt and the derived key are bytes.
    var dk: u8[] = crypto.pbkdf2_sha256("password", "salt".bytes(), 1, 32);
    if (dk.len() != 32) { return 12; }
    if (hex.hex_encode(dk) != "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b") { return 13; }
    if (!crypto.pbkdf2_verify("password", "salt".bytes(), 1, dk)) { return 14; }
    if (crypto.pbkdf2_verify("wrongpw", "salt".bytes(), 1, dk)) { return 15; }

    // HKDF, RFC 5869 Test Case 1. ` + "`info`" + ` is 0xf0..0xf9 — arbitrary
    // octets per the RFC, so it is u8[] too; as a string this canonical
    // vector would need an unchecked construction of non-UTF-8 bytes.
    var ikm: u8[] = __alloc_u8(22);
    var i: i32 = 0;
    while (i < 22) { ikm = ikm.with(i, 11 as u8); i = i + 1; }
    var salt: u8[] = [0 as u8,1 as u8,2 as u8,3 as u8,4 as u8,5 as u8,6 as u8,7 as u8,8 as u8,9 as u8,10 as u8,11 as u8,12 as u8];
    var info: u8[] = [240 as u8,241 as u8,242 as u8,243 as u8,244 as u8,245 as u8,246 as u8,247 as u8,248 as u8,249 as u8];
    var prk: u8[] = crypto.hkdf_extract(salt, ikm);
    if (prk.len() != 32) { return 16; }
    if (hex.hex_encode(prk) != "077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5") { return 17; }
    // extract's output feeds expand's prk directly — the coupling that
    // made this slice indivisible.
    var okm: u8[] = crypto.hkdf_expand(prk, info, 42);
    if (okm.len() != 42) { return 18; }
    if (hex.hex_encode(okm) != "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865") { return 19; }
    if (!crypto.consteq(okm, crypto.hkdf_sha256(salt, ikm, info, 42))) { return 20; }
    if (crypto.hkdf_sha256_hex(salt, ikm, info, 42) != hex.hex_encode(okm)) { return 21; }

    // TOTP: a base32-decoded secret is exactly what the key parameter
    // wants now, with no conversion in between (RFC 6238 App. B).
    var secret: u8[] = b32.base32_decode(b32.base32_encode("12345678901234567890123456789012".bytes()));
    if (crypto.totp_sha256(secret, 59, 30, 8) != 46119246) { return 22; }
    if (crypto.hotp_sha256(secret, 1, 8) != 46119246) { return 23; }

    return 0;
}
`

func TestCryptoBytesInterp(t *testing.T) {
	if got := runInterpExit(t, cryptoBytesProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestCryptoBytesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, cryptoBytesProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestCryptoBytesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, cryptoBytesProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestCryptoBytesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, cryptoBytesProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
