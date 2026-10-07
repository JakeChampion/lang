# Net P4: TLS 1.3 — the plan

How #9858 is built, in the order it lands. One PR per slice. Each slice
names its gate, and every primitive runs its vectors through the
interpreter and the self-host compiler on x86-64, arm64 and wasm.

## Module layout

`std/crypto.fern` is past the 4,000-line cap #9858 sets for any one
module, so each primitive is a module of its own under `std/crypto/`:
`std/crypto/chacha20poly1305`, then `aes_gcm`, `x25519`, `mlkem768`,
`p256`, `ed25519` and `rsa`. The existing digests, HMAC and HKDF stay in
`std/crypto`. TLS itself is `std/tls/record`, `handshake`, `client` and
`server`, with X.509 in `std/tls/der`, `verify` and `pem`. Every module
type-checks standalone (`TestStdlibModulesImportStandalone`).

## Slices

1. **ChaCha20-Poly1305** (RFC 8439). Pure Fern: `u32` ARX for the cipher,
   Poly1305 in 26-bit limbs with `u64` products. Gate: the RFC's vectors
   plus an independent implementation's, in the stdtest differential and
   `TestSelfHostChaCha20Poly1305Wasm`. About 94 MB/s on x86-64.
2. **The constant-time gate.** A builtin that marks a buffer secret emits
   valgrind's client-request sequence, which is a no-op outside valgrind.
   `perf.yml` runs the AEAD's kernels under memcheck on x86-64 and arm64,
   and a conditional jump or an address computed from a marked byte fails
   it. The builtin is classified four times like any other. This lands
   before the field arithmetic, so every later kernel is gated from its
   first PR.
3. **X25519** (RFC 7748). Field arithmetic mod 2^255 - 19 in ten limbs of
   25 and 26 bits held in `i64`, so every product fits, and a Montgomery
   ladder with a masked swap. Gate: RFC 7748's vectors and the 1,000-step
   iterated vector compiled (`TestSelfHostX25519Iterated1000`). About 0.2 ms
   per scalar multiplication on x86-64.
4. **AES-GCM.** Table-driven software AES leaks through the cache, so AES
   uses the instructions: `aes`/`pmull` on arm64, which the baseline has,
   and AES-NI/`pclmulqdq` on x86-64. AES-NI is outside the declared
   x86-64-v3 baseline, so this PR records the raise in CLAUDE.md and
   `docs/BACKEND-PARITY.md`. Every AVX2-class part has AES-NI, so reach is
   unchanged. wasm has neither, so it gets a bitsliced constant-time AES.
5. **ML-KEM-768** (FIPS 203) over `std/crypto`'s Keccak, which needs
   SHAKE128 and SHAKE256 added beside SHA-3. Then the X25519MLKEM768
   hybrid share.
6. **P-256 and Ed25519** for certificate signatures. P-256 verification
   landed first on `core/bigint`, about 13 ms per verification, since it
   handles only public values. Signing, for the server, needs constant-time
   32-bit limbs, which replace the bigint path. Ed25519 goes over slice 3's
   field and SHA-512.
7. **RSA-PSS and PKCS#1 v1.5 verify** over `core/bigint`. Verification
   handles only public values, so it does not need to be constant time,
   and it landed ahead of slices 4 to 6 for that reason: every public
   certificate chain needs it. About 1 ms per verification on x86-64.
8. **`std/tls/record` and `handshake`.** A sans-IO state machine in the
   rustls shape: bytes in, bytes out, no sockets. It runs identically on
   native, wasm and the sim. TLS 1.3 only. The key schedule uses HKDF from
   `std/crypto`.
9. **`std/tls/der`, `pem` and `verify`.** Path building, name and SAN
   checks, and the root store (Linux bundle paths, `SSL_CERT_FILE`, a
   bundled CCADB list as the fallback).
10. **`std/tls/client`.** X25519MLKEM768 by default, ALPN and
    HelloRetryRequest, wired into `fetch`'s `https`.
11. **`std/tls/server`.** Resumption and client certificates, wired into
    `std/serve`.
12. **Interop and fuzzing on `net-nightly`.** OpenSSL 3.5, Go, rustls'
    mio examples at a pinned tag, and BoringSSL's `bssl` at a pinned
    commit, plus the tlsfuzzer smoke set. A missing tool fails the lane.
    Handshakes per second and bulk throughput are recorded on the fixed
    host.

kTLS and the QUIC secret hooks come after slice 11. The handshake
interface in slice 8 keeps the secrets and handshake messages separable,
so HTTP/3 can be a package.
