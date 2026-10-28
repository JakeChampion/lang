# Cryptographic primitives and secret handling

Status: audit for [SEC-037](https://github.com/JakeChampion/lang/issues/13539).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The entropy contract is [ENTROPY.md](ENTROPY.md), and the erasure helper is in [ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md).

## What exists

Every primitive is an implementation of a published standard, and its suite checks the standard's vectors.
No module defines a cipher, a hash or a signature scheme of its own.

| Module | Standard | Vectors in `tests/stdlib` | Negative cases |
|---|---|---|---|
| `std/crypto/sha2`, `std/hash` | SHA-1, SHA-224, SHA-256, SHA-384, SHA-512, CRC32, cksum, BSD and SysV sums | the digest suites and `hash_checksums` | none needed, a digest has no verdict |
| `std/crypto` | HMAC-SHA-256, PBKDF2, HKDF, SHA3-224 to SHA3-512, SHAKE128 and SHAKE256 | `crypto` | none needed |
| `std/crypto/aes_gcm` | AES-GCM with the processor's AES and carry-less multiply instructions | `aes_gcm` | a flipped tag, ciphertext or additional-data bit is refused, and a wrong key length is refused |
| `std/crypto/chacha20poly1305` | RFC 8439 | `chacha20poly1305` | a flipped tag or ciphertext bit is refused, and a wrong key length is refused |
| `std/crypto/x25519` | RFC 7748 | `x25519` | the low-order points answer an error |
| `std/crypto/ed25519` | RFC 8032 | `ed25519` | a flipped signature bit, a flipped message, a non-canonical `S`, wrong lengths and undecodable keys are refused |
| `std/crypto/ecdsa`, `std/crypto/p256` | ECDSA over P-256 and P-384 with SHA-2, ECDH | `ecdsa`, `p256` | a flipped bit, another message, another key, another digest and malformed keys are refused |
| `std/crypto/rsa` | RSASSA-PKCS1-v1_5 and RSASSA-PSS verification | `rsa` | a flipped bit and malformed keys are refused |
| `std/crypto/mlkem768` | ML-KEM-768 (FIPS 203) | `mlkem768` | implicit rejection of a changed ciphertext, modulus and hash checks on the keys |
| `std/tls` | TLS 1.3 | the `tls_*` suites | the record, handshake and certificate suites refuse malformed and unauthenticated input |

The random source is `random_bytes`, which reads the kernel's entropy service and never returns a short result ([ENTROPY.md](ENTROPY.md)).
`std/rand` provides a seeded PCG32 generator, which its document describes as a reproducible sequence, not an entropy source.
`crypto.erase` zeroes an owned byte array's storage before the release.

## Constant time

A verdict that depends on a secret is computed without a branch and then declassified.
The AEAD open folds the tag difference into one byte, marks that byte public with `__ct_public`, and branches on it once.
`std/crypto/montgomery` provides constant-time Montgomery arithmetic for RSA and ML-KEM, and the curve modules declassify only the final verdict or the public result.

`__ct_secret(b)` tells Valgrind's memcheck that the bytes of `b` are undefined, and `__ct_public(b)` that they are defined again.
Memcheck then reports every branch and every address that depends on a secret byte.
`TestSelfHostCtGateX86_64` and `TestSelfHostCtGateArm64` run a program per primitive under memcheck. They require a clean run for AES-GCM, ChaCha20-Poly1305, X25519, ML-KEM-768, the TLS key share, Ed25519, ECDSA and P-256 ECDH.
Memcheck must report five small programs that branch or index on a secret, which proves the gate sees what it looks for.

## Remaining limits

- The constant-time gate observes one execution under memcheck. It does not prove the absence of a data-dependent timing in the processor's own instructions, such as a variable-latency multiply.
- RSA is verification only. There is no RSA signing and no RSA encryption.
- `crypto.erase` wipes an owned array the caller passes. A secret held in a struct field or a copy the compiler made is not wiped.
- The suites check published vectors and the listed negative cases. They are not a Wycheproof corpus, which covers many more malformed inputs per primitive.
- TLS trusts the root store it is given. Revocation checking is not implemented.
