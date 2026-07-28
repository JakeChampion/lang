package e2e

// cryptoVectorsProgram exercises std/crypto against standard SHA-256 and
// HMAC-SHA256 known-answer vectors (NIST / RFC 2104). It returns 0 on success
// or a small non-zero code identifying the failed vector. Used by the
// interp / x86-64 / arm64 / wasm crypto e2e tests (issue #2681).
const cryptoVectorsProgram = `
import "std/crypto";
import "std/string";
function main(): i32 {
    if (crypto.sha256_hex("") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") { return 1; }
    if (crypto.sha256_hex("abc") != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") { return 2; }
    // Multi-block message (>55 bytes forces a second compression block).
    if (crypto.sha256_hex("The quick brown fox jumps over the lazy dog") != "d7a8fbb307d7809469ca9abcb0082e4f8d5651e46d3cdb762d02d0bf37c9e592") { return 3; }
    // HMAC-SHA256 with a short key.
    if (crypto.hmac_sha256_hex("key".bytes(), "The quick brown fox jumps over the lazy dog") != "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8") { return 4; }
    // HMAC-SHA256 with a >64-byte key (exercises the key-is-hashed-first path).
    if (crypto.hmac_sha256_hex("this-is-a-deliberately-long-hmac-key-exceeding-sixty-four-bytes-so-it-gets-hashed-first".bytes(), "message") != "1c4000eb3d7dcdf15f3891fcffeb65b69d9d3dc114306505c3b2343cfdb08edd") { return 5; }
    return 0;
}
`
