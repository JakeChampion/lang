# Net P4: TLS 1.3 — the plan

How #9858 is built, in the order it lands. One PR per slice. Each slice
names its gate, and every primitive runs its vectors through the
interpreter and the self-host compiler on x86-64, arm64 and wasm.

## Module layout

`std/crypto.fern` is past the 4,000-line cap #9858 sets for any one
module, so each primitive is a module of its own under `std/crypto/`:
`std/crypto/chacha20poly1305`, then `aes_gcm`, `x25519`, `mlkem768`,
`p256`, `ed25519` and `rsa`, with the field X25519 and Ed25519 share in
`field25519`. The existing digests, HMAC and HKDF stay in `std/crypto`.
TLS itself is `std/tls/record`, `handshake`, `client` and `server`, with
X.509 in `std/tls/der`, `verify` and `pem`. Every module type-checks
standalone (`TestStdlibModulesImportStandalone`).

## Slices

1. **ChaCha20-Poly1305** (RFC 8439). Pure Fern: `u32` ARX for the cipher,
   Poly1305 in 26-bit limbs with `u64` products. Gate: the RFC's vectors
   plus an independent implementation's, in the stdtest differential and
   `TestSelfHostChaCha20Poly1305Wasm`. About 94 MB/s on x86-64.
2. **The constant-time gate.** Landed. `__ct_secret(b: [u8])` and
   `__ct_public(b: [u8])` issue memcheck's MAKE_MEM_UNDEFINED and
   MAKE_MEM_DEFINED client requests over the bytes, a no-op outside
   valgrind, and no-ops in both interpreters and on wasm. They lower to one
   op, `ct_mark`, whose register arms call `asmcore.rt_src_ct_mark`, a Fern
   helper that builds the request block in `__fern_scratch` and issues it
   through a new raw-floor op, `__raw_vg_request`: valgrind.h's sequence,
   the only per-backend code. Both are core with no capability. `open`
   declassifies only its tag verdict before branching on it.
   `TestSelfHostCtGateX86_64` runs the AEAD and probes under memcheck, and
   `perf.yml`'s bench job runs it with `TestSelfHostCtGateArm64` on both
   ISAs (docs/TEST-GATES.md). This lands before the field arithmetic, so
   every later kernel is gated from its first PR.
3. **X25519** (RFC 7748). Field arithmetic mod 2^255 - 19 in ten limbs of
   25 and 26 bits held in `i64`, so every product fits, and a Montgomery
   ladder with a masked swap. Gate: RFC 7748's vectors and the 1,000-step
   iterated vector compiled (`TestSelfHostX25519Iterated1000`). About 0.2 ms
   per scalar multiplication on x86-64.
4. **AES-GCM** (SP 800-38D). Landed. Table-driven software AES leaks
   through the cache, so `std/crypto/aes_gcm` (AES-128-GCM and AES-256-GCM)
   is built on three builtins with a constant-time kernel per backend:
   `__aes_expand_key`, `__aes_ctr32` (counter mode with GCM's 32-bit
   increment) and `__ghash`. arm64 uses `aese`/`aesmc` and `pmull`, which
   its baseline has; x86-64 uses AES-NI and `pclmulqdq`. AES-NI is outside
   x86-64-v3, so the baseline is now x86-64-v3 plus AES-NI (CLAUDE.md,
   `docs/BACKEND-PARITY.md`); every AVX2 part has it, so reach is unchanged.
   wasm has neither, so it runs a bitsliced AES over `i64` (the
   Boyar-Peralta S-box, after BearSSL's aes_ct64) and a GHASH multiplied a
   bit at a time under masks. Both interpreters spell the kernels from
   their definitions. Gate: FIPS-197 and the GCM specification's test cases
   in the stdtest differential and `TestSelfHostCryptoSuitesWasm`, each
   kernel against known answers on every target
   (`TestSelfHostAESGCMKernels*`), and the AEAD under the constant-time
   gate. About 1.1 GB/s sealing 64 KiB messages on x86-64 with `-O`, and
   about 25 MB/s on wasm under wasmtime.
5. **ML-KEM-768** (FIPS 203) over `std/crypto`'s Keccak, with SHAKE128 and
   SHAKE256 added beside SHA-3. Coefficients are `i32`s under Barrett
   reduction, and decapsulation compares and selects by mask. Gate: known
   answers from Go's FIPS 140 implementation, implicit rejection and the
   input checks. About 0.8 ms per key generation, encapsulation and
   decapsulation together on x86-64. The X25519MLKEM768 hybrid share comes
   with `std/tls/handshake`.
6. **P-256 and Ed25519** for certificate signatures. P-256 verification
   landed first on `core/bigint`, about 13 ms per verification, since it
   handles only public values. Signing, for the server, needs constant-time
   32-bit limbs, which replace the bigint path. Ed25519 signing and
   verification landed over slice 3's field, moved to
   `std/crypto/field25519` for both curves to share, and `std/crypto`'s
   SHA-512. Points are extended coordinates, signing multiplies the base
   point through a fixed 4-bit window that reads every table entry and keeps
   one by mask, and scalars mod L are 8-bit digits reduced without a branch.
   Verification is cofactorless, as in Go's crypto/ed25519. Gate: RFC 8032's
   vectors, one from Go, and the refusals, plus an `ed25519` case in
   `TestSelfHostCtGateX86_64` that signs under a secret seed. About 0.54 ms
   per signature and 0.28 ms per verification on x86-64.
7. **RSA-PSS and PKCS#1 v1.5 verify** over `core/bigint`. Verification
   handles only public values, so it does not need to be constant time,
   and it landed ahead of slices 4 to 6 for that reason: every public
   certificate chain needs it. About 1 ms per verification on x86-64.
8. **`std/tls/record` and `handshake`.** A sans-IO state machine in the
   rustls shape: bytes in, bytes out, no sockets. It runs identically on
   native, wasm and the sim. TLS 1.3 only. The key schedule uses HKDF from
   `std/crypto`.

   Landed: `std/tls/keyschedule`, the whole of RFC 8446 §7 and Finished's
   verify_data over HKDF-SHA256 and HKDF-SHA384, which `std/crypto` gained
   with HMAC-SHA384 for it; and `std/tls/record`, framing, deframing and
   ChaCha20-Poly1305 record protection with padding and sequence numbers.
   Gate: every derived value in RFC 8448 §3, ChaCha20-Poly1305 records from
   a reference that reproduces the trace's AES-128-GCM records, and every
   refusal, in the stdtest differential and `TestSelfHostCryptoSuitesWasm`.
   The AES-GCM suites are one `CipherSuite` arm and one `Aead` arm each once
   slice 4 lands. Remaining: `std/tls/handshake`, the state machine over
   these two modules, and the X25519MLKEM768 key share.
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
