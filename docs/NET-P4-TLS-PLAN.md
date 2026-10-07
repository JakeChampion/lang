# Net P4: TLS 1.3 — the plan

How #9858 is built, in the order it lands. One PR per slice. Each slice
names its gate, and every primitive runs its vectors through the
interpreter and the self-host compiler on x86-64, arm64 and wasm.

## Module layout

`std/crypto.fern` is past the 4,000-line cap #9858 sets for any one
module, so each primitive is a module of its own under `std/crypto/`:
`std/crypto/chacha20poly1305`, then `aes_gcm`, `x25519`, `mlkem768`,
`ecdsa`, `ed25519` and `rsa`, with the field X25519 and Ed25519 share in
`field25519`. The existing digests, HMAC and HKDF stay in `std/crypto`.
TLS itself is `std/tls/keyschedule`, `keyshare`, `message`, `record`,
`handshake`, `client` and `server`, with X.509 in `std/tls/der`, `pem`,
`x509` and `verify`. Every module type-checks standalone
(`TestStdlibModulesImportStandalone`).

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
   decapsulation together on x86-64. The X25519MLKEM768 hybrid share is
   `std/tls/keyshare`.
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
   record protection under all three suites with padding and sequence
   numbers. Gate: every derived value and protected record in RFC 8448 §3,
   ChaCha20-Poly1305 and AES-256-GCM records from a reference that
   reproduces the trace's, and every refusal, in the stdtest differential
   and `TestSelfHostCryptoSuitesWasm`. Then `std/tls/keyshare`, X25519 and
   the X25519MLKEM768 hybrid with caller-supplied randomness, gated on RFC
   8448's shares and Go's crypto/mlkem; and `std/tls/message`, every
   handshake message and the extensions TLS 1.3 reads, gated on RFC 8448's
   messages round-tripping byte for byte. Then `std/tls/handshake`, the
   client and server state machines: the caller judges the certificate
   (`VerifyServer`, `verdict`) and makes the signature (`SignNeeded`,
   `signature`), so no key or trust decision enters it. Gate: the client
   reproduces RFC 8448 §3 byte for byte, the server its ServerHello, and the
   two complete handshakes with each other across every suite and group.
   HelloRetryRequest comes with slice 10, resumption and client
   certificates with slice 11.
9. **`std/tls/der`, `pem`, `x509` and `verify`.** Path building, name and
   SAN checks, and the root store (Linux bundle paths, `SSL_CERT_FILE`, a
   bundled CCADB list as the fallback).

   Landed: strict DER, PEM, certificate parsing, and `verify_server`, which
   builds a path through the server's intermediates to the caller's roots
   at the caller's time. It checks validity, CA and path-length
   constraints, key usage, the leaf's server extended key usage, unknown
   critical extensions and every signature (RSA PKCS #1 v1.5 and PSS,
   ECDSA P-256 and P-384, Ed25519), and matches names against the subject
   alternative names only. `verify_signed` checks a CertificateVerify.
   Gate: chains made with Python's `cryptography` covering every refusal,
   and RFC 8448's certificate and CertificateVerify. Then name constraints
   over DNS names and IP addresses (other forms still refuse a critical
   extension), and the system root store: `SSL_CERT_FILE`, then the
   distributions' bundle paths. Remaining: the bundled CCADB list, as its own
   module so that only a program that asks for it carries it.
10. **`std/tls/client`.** X25519MLKEM768 by default, ALPN and
    HelloRetryRequest, wired into `fetch`'s `https`.

    Landed: HelloRetryRequest in `std/tls/handshake`, both sides. A server
    that takes none of the client's shares asks for one in a group the
    client named, and the client answers with a second ClientHello carrying
    that share and any cookie, the first ClientHello going into the
    transcript as its hash. Gate: client and server complete the retry
    against each other, the second ClientHello's share and cookie are
    checked, and a retry naming the group already shared, one not offered or
    one the client cannot make, a second retry, and a second ClientHello
    without the share are each refused. Then `std/tls/client`: a sans-IO
    `Session` that judges the chain against a root store and the
    CertificateVerify under the leaf's key, sends a failure's alert under
    the keys in force, and saves a connected session as bytes for a pool;
    and a `Connection` over a socket. `fetch` speaks `https` through it
    where it dials, offering `http/1.1` by ALPN and trusting the system's
    roots, keeps TLS connections in its pool, and reaches an `https` origin
    through `https_proxy` by a CONNECT tunnel. Real servers sign with P-384
    keys, so P-256 verification became `std/crypto/ecdsa` with P-384 beside
    it, and x509 and verify read and check both. Gate: the session against
    std/tls/handshake's server in the stdtest differential and on wasm, and
    `TestTLSClientAgainstGo`, which runs fetch and the socket client
    against Go's crypto/tls (P-256, RSA and Ed25519 leaves under a P-384 CA,
    a server forcing a HelloRetryRequest, an untrusted CA, a wrong name, a
    CONNECT proxy and a kept connection) on both native ISAs and in the
    interpreter.
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
