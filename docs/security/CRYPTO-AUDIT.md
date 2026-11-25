# Cryptographic primitives and secret handling

Status: audit for [SEC-037](https://github.com/JakeChampion/lang/issues/13539).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The entropy contract is [ENTROPY.md](ENTROPY.md), and the erasure helper is in [ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md).

## What exists

Every primitive is an implementation of a published standard, and its suite checks the standard's vectors.
No module defines a cipher, a hash or a signature scheme of its own.

| Module | Standard | Vectors in `tests/stdlib` | Negative cases |
|---|---|---|---|
| `std/crypto` | MD5, SHA-1, SHA-224, SHA-256, SHA-384, SHA-512, SM3, BLAKE2b, SHA-3 and SHAKE | `digest_*`, `crypto` | digest and output-length vectors |
| `std/crypto/sha2` | selects SHA-256, SHA-384 or SHA-512 and dispatches to `std/crypto` | SHA-2 signature vectors in `rsa` and `ecdsa` | unsupported signing hashes are covered in `ecdsa` |
| `std/hash` and checksum submodules | XXH64, CRC32, cksum, BSD and SysV sums | `hash_checksums` | noncryptographic checksums |
| `std/crypto` | HMAC and HKDF with SHA-256 or SHA-384, PBKDF2-HMAC-SHA256, HOTP and TOTP with SHA-256 | `crypto` | verification and parameter tests |
| `std/crypto/scrypt` | scrypt (RFC 7914) | `scrypt` | invalid costs, block sizes and parallelism |
| `std/crypto/aes_gcm` | AES-GCM with the processor's AES and carry-less multiply instructions | `aes_gcm` | a flipped tag, ciphertext or additional-data bit is refused, and a wrong key length is refused |
| `std/crypto/chacha20poly1305` | RFC 8439 | `chacha20poly1305` | a flipped tag or ciphertext bit is refused, and a wrong key length is refused |
| `std/crypto/x25519` | RFC 7748 | `x25519` | the low-order points answer an error |
| `std/crypto/ed25519` | RFC 8032 | `ed25519` | a flipped signature bit, a flipped message, a non-canonical `S`, wrong lengths and undecodable keys are refused |
| `std/crypto/ecdsa`, `std/crypto/p256` | ECDSA over P-256 and P-384 with SHA-2, ECDH | `ecdsa`, `p256` | a flipped bit, another message, another key, another digest and malformed keys are refused |
| `std/crypto/rsa` | RSASSA-PKCS1-v1_5 and RSASSA-PSS verification, RSAES-OAEP encryption and decryption | `rsa` | malformed keys, invalid signatures and OAEP decoding failures |
| `std/crypto/mlkem768` | ML-KEM-768 (FIPS 203) | `mlkem768` | implicit rejection of a changed ciphertext, modulus and hash checks on the keys |
| `std/crypto/hpke` | HPKE base mode with X-Wing, MLKEM768-P256 or DHKEM(P-256), HKDF-SHA256 and ChaCha20-Poly1305 | `hpke` | invalid public keys, lengths and ciphertexts |
| `std/tls` | TLS 1.3 and client-side TLS 1.2 | the `tls_*` suites | the record, handshake and certificate suites refuse malformed and unauthenticated input |

The random source is `random_bytes`, which reads the kernel's entropy service and never returns a short result ([ENTROPY.md](ENTROPY.md)).
`std/rand` provides a seeded PCG32 generator, which its document describes as a reproducible sequence, not an entropy source.
`crypto.erase` zeroes the storage of an owned array of bytes or of numeric words before the release.

## Wycheproof

The suites below run the Wycheproof `testvectors_v1` files.
`scripts/gen-wycheproof.py` writes them from the JSON files, and the header of each suite names its source and vector count.
The module must accept every valid vector and refuse every invalid vector. An acceptable vector runs for its verdict alone.
A module refuses a key, nonce or tag size it does not take. The AEAD suites check that a valid vector of such a size answers an error.

The ECDSA suites use the DER signatures, which the module reads as strict DER, so every BER variant is refused.
The std test gate runs the first four suites on both native targets and under the oracle.
The oracle takes about 25 minutes for ECDSA P-256 with SHA-256. `TestSelfHostWycheproofNative` runs the remaining suites on both native targets.

| Suite | Vectors | Groups |
|---|---|---|
| `wycheproof_aes_gcm` | 316 | 45, of which the 192-bit keys and the 64-bit and 128-bit nonces and the short tags are refused |
| `wycheproof_chacha20poly1305` | 325 | 10 |
| `wycheproof_x25519` | 518 | 9 |
| `wycheproof_ed25519` | 210 | 111 |
| `wycheproof_ecdsa_p256` | 484 | 113 |
| `wycheproof_ecdh_p256` | 355 | 12 |
| `wycheproof_ecdsa_p256_sha512` | 554 | 113 |
| `wycheproof_ecdsa_p384_sha384` | 504 | 105 |
| `wycheproof_hkdf_sha256` | 86 | 6 |
| `wycheproof_hmac_sha256` | 174 | 10 |
| `wycheproof_mlkem768` | 201 | 8 |
| `wycheproof_mlkem768_encaps` | 265 | 9 |
| `wycheproof_mlkem768_keygen` | 100 | 4 |
| `wycheproof_rsa_oaep` | 37 | 2 |
| `wycheproof_rsa_pkcs1v15` | 259 | 11 |
| `wycheproof_rsa_pss` | 108 | 4 |
| `wycheproof_xchacha20poly1305` | 315 | 10 |

## Constant time

A verdict that depends on a secret is computed without a branch and then declassified.
The AEAD open folds the tag difference into one byte, marks that byte public with `__ct_public`, and branches on it once.
`std/crypto/montgomery` provides constant-time Montgomery arithmetic for RSA and ML-KEM, and the curve modules declassify only the final verdict or the public result.

`__ct_secret(b)` tells Valgrind's memcheck that the bytes of `b` are undefined, and `__ct_public(b)` that they are defined again.
Memcheck then reports every branch and every address that depends on a secret byte.
`TestSelfHostCtGateX86_64` and `TestSelfHostCtGateArm64` run a program per primitive under memcheck. They require a clean run for AES-GCM, ChaCha20-Poly1305, X25519, ML-KEM-768, the TLS key share, Ed25519, ECDSA, P-256 ECDH, HPKE on the X-Wing path, HOTP-SHA256, XChaCha20-Poly1305, and RSA private-key construction with RSA-OAEP decryption.
The gate also checks equal-length matching and mismatching inputs to `consteq`, multi-block PBKDF2-HMAC-SHA256 and HKDF-SHA256/SHA384 outputs, and scrypt with a secret password. In ROMix the implementation marks the password-dependent lookup index public before reading its table.

The gate checks SHA-256 of a secret message, in one call and in parts.
It checks HMAC-SHA256 with a key longer than a block, and the eight lanes of `Sha256x8`.
It checks Argon2id with a secret password. The Argon2d part of Argon2id takes the reference of each block from the data, as RFC 9106 section 3.4 specifies. The implementation marks that word public before it selects the reference.
Memcheck must report the control programs that branch or index on a secret, which proves the gate sees what it looks for.

## Remaining limits

- The constant-time gate observes one execution under memcheck. It does not prove the absence of a data-dependent timing in the processor's own instructions, such as a variable-latency multiply.
- RSA supports signature verification and OAEP encryption and decryption. RSA signing is unavailable.
- `crypto.erase` wipes an owned array the caller passes. The key records of `std/crypto` are `@secret`, and their drop zeroes the arrays that they hold alone ([SECRET-TYPES.md](../SECRET-TYPES.md)). A copy the compiler made is not wiped.
- The primitives zero the key material they copy. HMAC zeroes its two pad blocks and a hashed long key. HKDF and PBKDF2 zero each output block and each message that carries one. ChaCha20-Poly1305 zeroes the one-time Poly1305 key. X25519 zeroes the clamped scalar of `public_key`. Ed25519 zeroes the nonce hash and the nonce scalar of a signature, and the limb array of each reduction. The ECDSA signer zeroes each nonce, and the key, the value and every message of its HMAC-DRBG. `TestSelfHostPrimitivesEraseKeyMaterial` scans the arena after each primitive for the copy it used to leave.
- The limb arithmetic of P-256, the generic Weierstrass curves and ML-KEM allocates an array for each intermediate product. A product of a secret is not zeroed. In the release build they stay in their released blocks until the allocator reuses them. Under `-harden` the allocator zeroes each block at its release ([ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md)).
- Wycheproof RSA coverage uses 2048-bit keys and SHA-256. Other RSA parameter sets and the standalone hash functions rely on the standards vectors and negative cases listed above.
- TLS trusts the root store it is given. Revocation checking is not implemented.
