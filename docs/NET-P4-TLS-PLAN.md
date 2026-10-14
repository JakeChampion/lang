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
   per signature and 0.28 ms per verification on x86-64. P-256 and P-384
   signing then landed in `std/crypto/ecdsa` over `std/crypto/montgomery`,
   a constant-time Montgomery field in 26-bit limbs, with complete
   projective addition, a masked 4-bit window and RFC 6979 nonces. Gate:
   RFC 6979's A.2.5 and A.2.6 signatures and a reference that reproduces
   them, every result verified, the field against `core/bigint`, and an
   `ecdsa` case in `TestSelfHostCtGateX86_64` signing on both curves under
   a secret scalar.
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
   distributions' bundle paths. Then `std/tls/mozilla`, the roots Mozilla
   trusts for server authentication as one PEM string generated by
   `cmd/rootsgen` from the NSS certdata.txt committed beside it (CCADB's
   list reaches programs as that file): its own module, so only a program
   that imports it carries the 220 KB, handed to `fetch` as
   `Policy.roots`. A root Mozilla distrusts for server certificates issued
   after a date is bundled with the date (`verify.Roots.distrust_after`,
   which `verify_server` enforces against the leaf's notBefore) while a
   certificate issued before it can still be valid, and left out once none
   can; the refresh-data workflow regenerates the list weekly. Gate: every
   bundled root parses, and raw.githubusercontent.com's chain verifies
   against them.
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

    Landed: `std/tls/server`, a sans-IO `Session` over the handshake's
    server that reads a P-256, P-384 or Ed25519 key from PKCS #8 or SEC 1,
    checks it against the leaf, and signs the CertificateVerify; and a
    `Connection` over an accepted socket. RSA keys are refused until
    std/crypto/rsa can sign in constant time. Gate: the session against
    std/tls/client over all three key types in the stdtest differential and
    on wasm, and `TestTLSServerAgainstGo`, Go's crypto/tls client against a
    Fern echo server with each leaf (default shares, a P-256-only share
    drawing a HelloRetryRequest, an address with no SNI, an untrusted CA
    and a TLS 1.2 client) on both native ISAs and in the interpreter.

    Then resumption (§4.6.1, §4.2.11): `std/tls/message` gained
    `pre_shared_key`, `std/tls/keyschedule` the binder key and the PSK a
    ticket stands for (checked against RFC 8448 §4), and the handshake
    offers a ticket with its binder (after a HelloRetryRequest too), asks
    the server's caller to look one up (`ResumeOffered`, `resume`), checks
    the binder, and skips the certificate. `std/tls/server` issues a ticket
    per connection, sealed under a per-config key shared by
    `with_ticket_key`, and `std/tls/client` hands tickets to the caller in
    `Step.tickets` and offers one back with `with_ticket` to the name it was
    issued to. Gate: the handshake, client and server suites, and both Go
    interop tests resuming in each direction.

    Then client certificates (§4.4.2): the key and chain reading moved from
    `std/tls/server` to `std/tls/identity`, which both sides present; the
    handshake's server sends a CertificateRequest when asked to (never on
    a resumed handshake), puts the client's chain and CertificateVerify to
    its caller (`VerifyClient`, `verdict`), and its client sends its chain
    and asks its caller for the signature; `std/tls/verify` gained
    `verify_client`; `std/tls/server` requires a chain reaching
    `with_client_roots` (certificate_required for none, bad_certificate
    for a bad one) and `std/tls/client` presents `with_identity`. Gate: the
    handshake, client and server suites, and both Go interop tests with a
    client certificate each way.

    Then `std/serve`: `Config.with_tls(cfg)` runs every connection
    through a `std/tls/server` session in the loop's own read and write
    paths, with the handshake on the connection's reads before its first
    request, responses sealed as the builder is written out, a file body
    read and sealed in place of sendfile, and a streamed body pulled
    through the session the handler holds while it reads. Gate:
    `TestServeTLSAgainstGo` (Go's net/http client: a kept connection, an
    upload, a response of many records, a file body, a streamed body with
    Expect: 100-continue, a resumed connection, a plaintext client
    refused) in the interpreter and on x86-64.
12. **Interop and fuzzing on `net-nightly`.** OpenSSL 3.5, Go, rustls'
    mio examples at a pinned tag, and BoringSSL's `bssl` at a pinned
    commit, plus the tlsfuzzer smoke set. A missing tool fails the lane.
    Handshakes per second and bulk throughput are recorded on the fixed
    host.

    Landed, the matrix: `scripts/net-tls-interop` and the
    `net-tls-interop-x86_64` job in `net-nightly.yml`. The Fern client
    runs against OpenSSL's `s_server`, Go's crypto/tls, rustls'
    `tlsserver-mio` at the tag the script pins and BoringSSL's `bssl
    server` at the commit it pins; the Fern server
    (`scripts/net-bench.d/hello.fern` under `TLS_CERT`, `TLS_KEY` and
    `TLS_CLIENT_CA`) under Go's client, curl, `openssl s_client`,
    `tlsclient-mio` and `bssl client`: a GET, a kept connection, a
    session resumed from its ticket, a client certificate shown and
    withheld, each way where the peer's tool can. The runner's OpenSSL
    is the image's package (3.0 today), which does not know
    X25519MLKEM768, so against it every Fern-client handshake takes
    HelloRetryRequest; the other three take the hybrid share directly,
    so both paths run every night. rustls' examples and bssl are built
    from source once per pin and cached. The report records handshakes
    per second (`openssl s_time`, full and resumed, and the client's own
    twenty against each server) and bulk throughput (a 16 MiB body each
    way with OpenSSL, Go and curl) under lane `net-tls-interop` in
    `refs/notes/perf`.

    tlsfuzzer's first finding came before its lane did: its TLS 1.3
    scripts offer a secp256r1 key share by default, and every one ended
    in handshake_failure because `std/tls/keyshare` had only X25519 and
    X25519MLKEM768, while RFC 8446 §9.1 makes secp256r1 the one key
    exchange every implementation must have. `keyshare.Secp256r1`
    (0x0017) now rides `ecdsa.ecdh`, the signer's constant-time
    multiplication over the peer's point, with a 40-byte draw reduced
    modulo the order so no draw is refused; both sides name it after
    X25519, so a server with only P-256 gets there by HelloRetryRequest.
    Gate: RFC 5903 §8.1's exchange in the keyshare and ecdsa suites, the
    handshake suite, and `TestSelfHostCtGateX86_64/p256-ecdh`. With the
    P-256 half in place, SecP256r1MLKEM768 (0x11eb) is the same hybrid
    composition as X25519MLKEM768 with the P-256 part first, and the
    server's entropy is sized from its groups (`server_entropy(config)`)
    rather than fixed. The same scripts found two more: a client
    CertificateVerify that does not verify was refused with
    bad_certificate where §4.4.3 says decrypt_error, and one under a
    scheme the client's key cannot make with bad_certificate where §4.2.3
    says illegal_parameter; and a socket `Connection` whose `recv` had
    failed still sent close_notify from `close()` after the fatal alert,
    where nothing may follow a fatal alert, so a failed exchange now
    closes the socket and `close()` after it does nothing.

    Landed, the fuzzing: `scripts/net-tls-fuzz` and the
    `net-tls-fuzz-x86_64` job, tlsfuzzer's TLS 1.3 scripts at a pinned
    commit against `scripts/net-tls-fuzz.d/server.fern` in four
    instances. `scripts/net-tls-fuzz.d/cases` is the set, fifteen scripts
    with every excluded probe and its reason beside it: a probe
    presupposing a curve, scheme or KEM the stack does not have is
    excluded, and one where the stack's behaviour is the right one
    (RFC 8446 lets a server drop repeated compatibility CCS records and
    says unexpected_message for a plaintext record) is expected to fail,
    so a change in either direction is a red nightly. The 38 scripts
    whose handshakes offer only RSA signature schemes are outside the
    set: std/tls verifies RSA and does not sign with it, and the
    constant-time gate is the reason it does not.

kTLS and the QUIC secret hooks come after slice 11. The handshake
interface in slice 8 keeps the secrets and handshake messages separable,
so HTTP/3 can be a package.

The QUIC hooks (landed): `std/tls/handshake` runs under QUIC when the
config says so. Handshake bytes come in by encryption level through
`read_crypto` and go out as `Crypto` events for CRYPTO frames; every
traffic secret is reported as a `Secret` event as it is set, by level and
direction, so QUIC's packet protection derives its keys from it under the
negotiated suite; the transport parameters ride as the extensions the
config adds and `peer_extensions()` answers, and RFC 9001's terms hold
(transport parameters and ALPN on both sides, no KeyUpdate, no
application data, no alert record, no compatibility CCS). An HTTP/3
package owns the packets, frames, flow control and recovery; nothing in
`std/tls` changes for the record-layer client and server.

kTLS (landed): an offloaded session hands its
record layer to Linux's (`std/tls/ktls`). The handshake runs in userspace
as before; `offload()` then gives the kernel each direction's key, IV and
next sequence number in its own structure
(`record.Protection.kernel_info`), only at a record boundary with nothing
read but unprocessed, since the kernel takes the stream from the next
record. Afterwards application data is plain bytes on the socket, and the
other records still pass through the session, which frames what it writes
in the clear and takes what the kernel opened framed as it arrived: an
alert, a ticket, and a KeyUpdate, whose new secret comes back in the step's
`rekeyed` for `install`. A kernel before 6.14 refuses a second key in a
direction, so there a KeyUpdate ends the connection with `KeysRefused`.
`update_keys(request)` sends a KeyUpdate from either side, offloaded or
not. Measured in a qemu VM before CI, on Ubuntu's 6.8 and 6.17 kernels:
the three suites round-trip both ways against the record layer, and Go's
crypto/tls drives both ends, with the rekey both ways on 6.17 and refused
on 6.8. The x86-64 e2e lane loads the `tls` module and runs
`TestX86_64KtlsRecords` and `TestX86_64KtlsAgainstGo`, which elsewhere
skip. `std/serve` offloads its TLS connections under `Config.ktls`,
right after the read that completes the handshake: its plain read path
then serves them, an EIO from it is the next control record for the
session, and a file body goes out by sendfile. Go's net/http client
drives the serve TLS world with it (`TestX86_64KtlsServe`; in the VM on
both kernels, two connections per run taken by the kernel both ways).
`std/fetch` offloads each https connection it opens under
`Request.ktls`, in `__secured` once the handshake is done; the
`Transport` names the socket under a connection (`socket`, -1 for the
simulator's), and an offloaded session's empty read is told apart from
the end of the stream by the kernel's typed read. The HTTP/2 connections
a serve loop drives for its handlers take the same path. Go's servers
drive it through `TestTLSClientAgainstGo`'s world, all eight completed
handshakes offloaded (`TestX86_64KtlsFetch`), and the serve loop's shared
upstream through a tunnel (`TestX86_64KtlsServeH2`), in the VM on both
kernels.
