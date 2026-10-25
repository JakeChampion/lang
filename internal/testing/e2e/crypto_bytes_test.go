package e2e

import "testing"

const cryptoBytesProgram = `
import "std/crypto" as crypto;
import "std/hex" as hex;
import "std/base32" as b32;
import "std/utf8" as utf8;
import "std/string";

function main(): i32 {
    // A digest is u8[], 32 bytes wide.
    let d: u8[] = crypto.sha256("abc".as_bytes());
    if (d.len() != 32) { return 1; }
    if (d[0] as i32 != 186) { return 2; }   // 0xba — SHA-256("abc") starts ba78…

    // ...and it is NOT valid UTF-8, which is the whole reason it is not
    // a string: the checked constructor rejects it.
    match (utf8.from_bytes(d)) {
        Some(s) => { return 3; },
        None => {}
    }

    // The one-shot digest agrees with the streaming hasher.
    if (hex.encode(d) != hex.encode(crypto.sha256_new().update("abc".as_bytes()).finish())) { return 4; }
    if (hex.encode(d) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") { return 5; }

    // The HMAC key and message borrow byte views.
    let key: u8[] = "key".bytes();
    let mac: u8[] = crypto.hmac_sha256(key, "The quick brown fox jumps over the lazy dog".as_bytes());
    if (mac.len() != 32) { return 6; }
    if (hex.encode(mac) != "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8") { return 7; }

    // consteq compares u8[], so hmac_verify takes the digest directly.
    if (!crypto.consteq(mac, mac)) { return 8; }
    if (crypto.consteq(mac, d)) { return 9; }
    if (!crypto.hmac_sha256_verify(key, "The quick brown fox jumps over the lazy dog".as_bytes(), mac)) { return 10; }
    if (crypto.hmac_sha256_verify("wrongkey".bytes(), "The quick brown fox jumps over the lazy dog".as_bytes(), mac)) { return 11; }

    // PBKDF2 borrows the password and salt bytes. The derived key owns its bytes.
    let dk: u8[] = crypto.pbkdf2_sha256("password".as_bytes(), "salt".bytes(), 1, 32);
    if (dk.len() != 32) { return 12; }
    if (hex.encode(dk) != "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b") { return 13; }
    if (!crypto.pbkdf2_sha256_verify("password".as_bytes(), "salt".bytes(), 1, dk)) { return 14; }
    if (crypto.pbkdf2_sha256_verify("wrongpw".as_bytes(), "salt".bytes(), 1, dk)) { return 15; }

    // HKDF, RFC 5869 Test Case 1. ` + "`info`" + ` is 0xf0..0xf9 — arbitrary
    // octets per the RFC, so it is u8[] too; as a string this canonical
    // vector would need an unchecked construction of non-UTF-8 bytes.
    let ikm: u8[] = __alloc_u8(22);
    let i: i32 = 0;
    while (i < 22) { ikm = ikm.with(i, 11 as u8); i = i + 1; }
    let salt: u8[] = [0 as u8,1 as u8,2 as u8,3 as u8,4 as u8,5 as u8,6 as u8,7 as u8,8 as u8,9 as u8,10 as u8,11 as u8,12 as u8];
    let info: u8[] = [240 as u8,241 as u8,242 as u8,243 as u8,244 as u8,245 as u8,246 as u8,247 as u8,248 as u8,249 as u8];
    let prk: u8[] = crypto.hkdf_sha256_extract(salt, ikm);
    if (prk.len() != 32) { return 16; }
    if (hex.encode(prk) != "077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5") { return 17; }
    // extract's output feeds expand's prk directly — the coupling that
    // made this slice indivisible.
    let okm: u8[] = crypto.hkdf_sha256_expand(prk, info, 42);
    if (okm.len() != 42) { return 18; }
    if (hex.encode(okm) != "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865") { return 19; }
    if (!crypto.consteq(okm, crypto.hkdf_sha256(salt, ikm, info, 42))) { return 20; }
    if (hex.encode(crypto.hkdf_sha256(salt, ikm, info, 42)) != hex.encode(okm)) { return 21; }

    // TOTP: a base32-decoded secret is exactly what the key parameter
    // wants now, with no conversion in between (RFC 6238 App. B).
    let Ok(secret) = b32.decode(b32.encode("12345678901234567890123456789012".bytes())) else { return 24; };
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
