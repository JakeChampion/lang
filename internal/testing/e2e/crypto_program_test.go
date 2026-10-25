package e2e

const cryptoVectorsProgram = `
import "std/crypto";
import "std/hex";
import "std/string";
function main(): i32 {
    if (hex.encode(crypto.sha256("".as_bytes())) != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") { return 1; }
    if (hex.encode(crypto.sha256("abc".as_bytes())) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") { return 2; }
    // Multi-block message (>55 bytes forces a second compression block).
    if (hex.encode(crypto.sha256("The quick brown fox jumps over the lazy dog".as_bytes())) != "d7a8fbb307d7809469ca9abcb0082e4f8d5651e46d3cdb762d02d0bf37c9e592") { return 3; }
    // HMAC-SHA256 with a short key.
    if (hex.encode(crypto.hmac_sha256("key".bytes(), "The quick brown fox jumps over the lazy dog".as_bytes())) != "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8") { return 4; }
    // HMAC-SHA256 with a >64-byte key (exercises the key-is-hashed-first path).
    if (hex.encode(crypto.hmac_sha256("this-is-a-deliberately-long-hmac-key-exceeding-sixty-four-bytes-so-it-gets-hashed-first".bytes(), "message".as_bytes())) != "1c4000eb3d7dcdf15f3891fcffeb65b69d9d3dc114306505c3b2343cfdb08edd") { return 5; }
    return 0;
}
`
