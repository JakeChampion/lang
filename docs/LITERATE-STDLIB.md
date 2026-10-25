# Literate standard library sources

Status: [reference] shipped.

The standard library has 158 source modules. Of these, 152 use literate Fern documents.
These documents contain handwritten code, STE prose, and named chunks.
Seven documents also contain generated regions. Each generated region stays inside one complete chunk with its boundary markers intact.

The library has 71 executable examples across 68 documents.
The root chunk preserves the original declaration order. Module imports retain their public names.
The compiler expands each source document in memory and maps diagnostics back to its document lines.

## Source edits and checks

Edit module prose in its Markdown document. Edit code inside its named chunks.
Keep the root references in declaration order. Keep generator boundary markers inside one chunk.
Declaration comments supply API descriptions and editor hover text.

Use these commands for one document:

```sh
fern -fmt -w internal/stdlib/std/hex.fern.md
fern -doctest internal/stdlib/std/hex.fern.md
fern -tangle internal/stdlib/std/hex.fern.md
fern -weave -html -o build/hex.html internal/stdlib/std/hex.fern.md
```

Run `make check-sources` for standalone import checks.
Run `make digest-check log-table-check huffman-table-check` after edits to their generated regions.
The float-table and time-zone generator tests also check their committed source documents.

The Go formatter corpus expands source documents into temporary plain modules.
This keeps its AST, format stability, and comment checks active for every literate module.
The standard-library doctest test discovers all documents with test fences.

## Plain sources

Four modules have generators that own the complete file. They retain plain Fern sources:

| Module | Generator |
| --- | --- |
| `std/brotli/dictionary` | `cmd/brotlidictgen` |
| `std/publicsuffix` | `cmd/pslgen` |
| `std/tls/mozilla` | `cmd/rootsgen` |
| `std/unicode` | `cmd/unicodegen` |

`core/_test_empty` and `std/_test_empty` are plain test helpers.
They are outside the public library inventory below.

## Literate modules

| Module | Source document |
| --- | --- |
| `core/bigint` | [Arbitrary-precision integers](../internal/stdlib/core/bigint.fern.md) |
| `core/cmp` | [Comparison and display traits](../internal/stdlib/core/cmp.fern.md) |
| `core/int` | [Integer text and radix conversion](../internal/stdlib/core/int.fern.md) |
| `core/iter` | [Value iterators and lazy adapters](../internal/stdlib/core/iter.fern.md) |
| `core/map` | [The map runtime](../internal/stdlib/core/map.fern.md) |
| `core/mem` | [Value lifetime hooks](../internal/stdlib/core/mem.fern.md) |
| `std/ansi` | [ANSI terminal styles](../internal/stdlib/std/ansi.fern.md) |
| `std/array` | [Array operations and statistics](../internal/stdlib/std/array.fern.md) |
| `std/async` | [Futures and structured concurrency](../internal/stdlib/std/async.fern.md) |
| `std/base32` | [Base32 text and byte data](../internal/stdlib/std/base32.fern.md) |
| `std/base64` | [Base64 text and byte data](../internal/stdlib/std/base64.fern.md) |
| `std/bench` | [Time and allocation measurements](../internal/stdlib/std/bench.fern.md) |
| `std/binary` | [Network-order integer fields](../internal/stdlib/std/binary.fern.md) |
| `std/brotli` | [Brotli decompression](../internal/stdlib/std/brotli.fern.md) |
| `std/cli` | [Command-line specifications](../internal/stdlib/std/cli.fern.md) |
| `std/codec` | [Shared codec errors](../internal/stdlib/std/codec.fern.md) |
| `std/constmap` | [Immutable maps for known string keys](../internal/stdlib/std/constmap.fern.md) |
| `std/convert` | [Value conversion traits](../internal/stdlib/std/convert.fern.md) |
| `std/crypto` | [Digests, authentication codes, and key derivation](../internal/stdlib/std/crypto.fern.md) |
| `std/crypto/aead` | [Authenticated-encryption errors](../internal/stdlib/std/crypto/aead.fern.md) |
| `std/crypto/aes_gcm` | [AES-GCM authenticated encryption](../internal/stdlib/std/crypto/aes_gcm.fern.md) |
| `std/crypto/chacha20poly1305` | [ChaCha20-Poly1305 authenticated encryption](../internal/stdlib/std/crypto/chacha20poly1305.fern.md) |
| `std/crypto/ecdsa` | [ECDSA signatures and key agreement](../internal/stdlib/std/crypto/ecdsa.fern.md) |
| `std/crypto/ed25519` | [Ed25519 signatures](../internal/stdlib/std/crypto/ed25519.fern.md) |
| `std/crypto/field25519` | [Arithmetic for Curve25519](../internal/stdlib/std/crypto/field25519.fern.md) |
| `std/crypto/mlkem768` | [ML-KEM-768 key encapsulation](../internal/stdlib/std/crypto/mlkem768.fern.md) |
| `std/crypto/montgomery` | [Montgomery modular arithmetic](../internal/stdlib/std/crypto/montgomery.fern.md) |
| `std/crypto/p256` | [NIST P-256 arithmetic](../internal/stdlib/std/crypto/p256.fern.md) |
| `std/crypto/rsa` | [RSA signature verification](../internal/stdlib/std/crypto/rsa.fern.md) |
| `std/crypto/sha2` | [SHA-2 selection for signatures](../internal/stdlib/std/crypto/sha2.fern.md) |
| `std/crypto/x25519` | [X25519 key agreement](../internal/stdlib/std/crypto/x25519.fern.md) |
| `std/csv` | [CSV records](../internal/stdlib/std/csv.fern.md) |
| `std/deflate` | [DEFLATE, zlib, and gzip](../internal/stdlib/std/deflate.fern.md) |
| `std/dns` | [DNS messages and resolution](../internal/stdlib/std/dns.fern.md) |
| `std/dotenv` | [Environment-file values](../internal/stdlib/std/dotenv.fern.md) |
| `std/envflag` | [Boolean environment flags](../internal/stdlib/std/envflag.fern.md) |
| `std/errno` | [Portable runtime error numbers](../internal/stdlib/std/errno.fern.md) |
| `std/error` | [The common error interface](../internal/stdlib/std/error.fern.md) |
| `std/f32` | [Binary32 parsing](../internal/stdlib/std/f32.fern.md) |
| `std/f64` | [Floating-point operations and text](../internal/stdlib/std/f64.fern.md) |
| `std/fetch` | [HTTP client requests](../internal/stdlib/std/fetch.fern.md) |
| `std/format` | [Text format helpers](../internal/stdlib/std/format.fern.md) |
| `std/fuzz` | [Byte-buffer fuzz tests](../internal/stdlib/std/fuzz.fern.md) |
| `std/glob` | [Shell-style pattern matches](../internal/stdlib/std/glob.fern.md) |
| `std/hash` | [Checksums and fast hashes](../internal/stdlib/std/hash.fern.md) |
| `std/headers` | [HTTP header values](../internal/stdlib/std/headers.fern.md) |
| `std/hex` | [Hexadecimal text and bytes](../internal/stdlib/std/hex.fern.md) |
| `std/http` | [HTTP requests and responses](../internal/stdlib/std/http.fern.md) |
| `std/http/accesslog` | [HTTP access log records](../internal/stdlib/std/http/accesslog.fern.md) |
| `std/http/auth` | [HTTP authentication helpers](../internal/stdlib/std/http/auth.fern.md) |
| `std/http/cache` | [HTTP cache validators](../internal/stdlib/std/http/cache.fern.md) |
| `std/http/compress` | [HTTP response compression](../internal/stdlib/std/http/compress.fern.md) |
| `std/http/cookie` | [HTTP cookies and cookie storage](../internal/stdlib/std/http/cookie.fern.md) |
| `std/http/cors` | [Cross-origin request policy](../internal/stdlib/std/http/cors.fern.md) |
| `std/http/form` | [URL-encoded request forms](../internal/stdlib/std/http/form.fern.md) |
| `std/http/forwarded` | [Clients behind trusted proxies](../internal/stdlib/std/http/forwarded.fern.md) |
| `std/http/grpc` | [gRPC messages over HTTP/2](../internal/stdlib/std/http/grpc.fern.md) |
| `std/http/h2` | [HTTP/2 connection state](../internal/stdlib/std/http/h2.fern.md) |
| `std/http/h2/frame` | [HTTP/2 frame representations](../internal/stdlib/std/http/h2/frame.fern.md) |
| `std/http/h3` | [HTTP/3 session state](../internal/stdlib/std/http/h3.fern.md) |
| `std/http/h3q` | [HTTP/3 over QUIC](../internal/stdlib/std/http/h3q.fern.md) |
| `std/http/hpack` | [HPACK header compression](../internal/stdlib/std/http/hpack.fern.md) |
| `std/http/huffman` | [The HPACK and QPACK Huffman code](../internal/stdlib/std/http/huffman.fern.md) |
| `std/http/multipart` | [Multipart form-data streams](../internal/stdlib/std/http/multipart.fern.md) |
| `std/http/qpack` | [QPACK field compression](../internal/stdlib/std/http/qpack.fern.md) |
| `std/http/requestid` | [HTTP request identifiers](../internal/stdlib/std/http/requestid.fern.md) |
| `std/http/router` | [HTTP route selection](../internal/stdlib/std/http/router.fern.md) |
| `std/http/sse` | [Server-sent event text](../internal/stdlib/std/http/sse.fern.md) |
| `std/http/static` | [Static HTTP file responses](../internal/stdlib/std/http/static.fern.md) |
| `std/http/trace` | [W3C Trace Context](../internal/stdlib/std/http/trace.fern.md) |
| `std/http/websocket` | [WebSocket server sessions](../internal/stdlib/std/http/websocket.fern.md) |
| `std/i32` | [32-bit integers and byte properties](../internal/stdlib/std/i32.fern.md) |
| `std/i64` | [64-bit integer methods](../internal/stdlib/std/i64.fern.md) |
| `std/io` | [Reader and input helpers](../internal/stdlib/std/io.fern.md) |
| `std/io_buffered` | [Buffered byte input and output](../internal/stdlib/std/io_buffered.fern.md) |
| `std/jni` | [Java Native Interface calls](../internal/stdlib/std/jni.fern.md) |
| `std/json` | [JSON trees and documents](../internal/stdlib/std/json.fern.md) |
| `std/log` | [Structured log entries](../internal/stdlib/std/log.fern.md) |
| `std/math` | [Ranges, numeric constants, and RGB values](../internal/stdlib/std/math.fern.md) |
| `std/metrics` | [Prometheus metric values](../internal/stdlib/std/metrics.fern.md) |
| `std/mock_platform` | [Recorded platform operations](../internal/stdlib/std/mock_platform.fern.md) |
| `std/ndarray` | [Multidimensional array views](../internal/stdlib/std/ndarray.fern.md) |
| `std/net` | [Network address values](../internal/stdlib/std/net.fern.md) |
| `std/number_parse` | [Numeric parse errors and kernels](../internal/stdlib/std/number_parse.fern.md) |
| `std/num` | [Arithmetic trait contracts](../internal/stdlib/std/num.fern.md) |
| `std/option` | [Optional values](../internal/stdlib/std/option.fern.md) |
| `std/ordmap` | [Persistent ordered maps](../internal/stdlib/std/ordmap.fern.md) |
| `std/ordset` | [Persistent ordered sets](../internal/stdlib/std/ordset.fern.md) |
| `std/path` | [POSIX path text](../internal/stdlib/std/path.fern.md) |
| `std/peg` | [Parsing expression grammars](../internal/stdlib/std/peg.fern.md) |
| `std/platform` | [Handler platform capabilities](../internal/stdlib/std/platform.fern.md) |
| `std/pmap` | [Persistent hash maps](../internal/stdlib/std/pmap.fern.md) |
| `std/png` | [RGB PNG output](../internal/stdlib/std/png.fern.md) |
| `std/protobuf` | [Protocol Buffers wire fields](../internal/stdlib/std/protobuf.fern.md) |
| `std/pset` | [Persistent hash sets](../internal/stdlib/std/pset.fern.md) |
| `std/pvec` | [Persistent indexed vectors](../internal/stdlib/std/pvec.fern.md) |
| `std/quic/conn` | [QUIC connection state](../internal/stdlib/std/quic/conn.fern.md) |
| `std/quic/frames` | [QUIC frames and variable-length integers](../internal/stdlib/std/quic/frames.fern.md) |
| `std/quic/packet` | [Protected QUIC packets](../internal/stdlib/std/quic/packet.fern.md) |
| `std/quic/params` | [QUIC transport parameters](../internal/stdlib/std/quic/params.fern.md) |
| `std/quic/recovery` | [QUIC loss and congestion state](../internal/stdlib/std/quic/recovery.fern.md) |
| `std/quic/streams` | [QUIC stream flow control](../internal/stdlib/std/quic/streams.fern.md) |
| `std/rand` | [Random choices and seeded sequences](../internal/stdlib/std/rand.fern.md) |
| `std/regex` | [Regular expression evaluation](../internal/stdlib/std/regex.fern.md) |
| `std/result` | [Results and errors](../internal/stdlib/std/result.fern.md) |
| `std/semver` | [Semantic version values](../internal/stdlib/std/semver.fern.md) |
| `std/serve` | [HTTP server execution](../internal/stdlib/std/serve.fern.md) |
| `std/set` | [Generic distinct-value sets](../internal/stdlib/std/set.fern.md) |
| `std/signal` | [Process signal policy](../internal/stdlib/std/signal.fern.md) |
| `std/sim` | [Deterministic asynchronous execution](../internal/stdlib/std/sim.fern.md) |
| `std/sim_fetch` | [Scripted HTTP network responses](../internal/stdlib/std/sim_fetch.fern.md) |
| `std/sim_platform` | [Platform capabilities under simulation](../internal/stdlib/std/sim_platform.fern.md) |
| `std/simd` | [Generic scalar and vector kernels](../internal/stdlib/std/simd.fern.md) |
| `std/strdist` | [String edit distance](../internal/stdlib/std/strdist.fern.md) |
| `std/stream` | [Asynchronous byte streams](../internal/stdlib/std/stream.fern.md) |
| `std/string` | [String operations and numeric text](../internal/stdlib/std/string.fern.md) |
| `std/table` | [Text table layout](../internal/stdlib/std/table.fern.md) |
| `std/test` | [TAP test results](../internal/stdlib/std/test.fern.md) |
| `std/textwrap` | [Text line widths](../internal/stdlib/std/textwrap.fern.md) |
| `std/time` | [Dates, times, and durations](../internal/stdlib/std/time.fern.md) |
| `std/tls` | [TLS failure values](../internal/stdlib/std/tls.fern.md) |
| `std/tls/client` | [TLS 1.3 client sessions](../internal/stdlib/std/tls/client.fern.md) |
| `std/tls/der` | [DER byte values](../internal/stdlib/std/tls/der.fern.md) |
| `std/tls/handshake` | [TLS 1.3 handshake state](../internal/stdlib/std/tls/handshake.fern.md) |
| `std/tls/identity` | [TLS certificate and key material](../internal/stdlib/std/tls/identity.fern.md) |
| `std/tls/keyschedule` | [TLS 1.3 secret derivation](../internal/stdlib/std/tls/keyschedule.fern.md) |
| `std/tls/keyshare` | [TLS 1.3 key shares](../internal/stdlib/std/tls/keyshare.fern.md) |
| `std/tls/ktls` | [Kernel TLS socket operations](../internal/stdlib/std/tls/ktls.fern.md) |
| `std/tls/message` | [TLS records and handshake messages](../internal/stdlib/std/tls/message.fern.md) |
| `std/tls/pem` | [PEM text blocks](../internal/stdlib/std/tls/pem.fern.md) |
| `std/tls/record` | [TLS 1.3 record protection](../internal/stdlib/std/tls/record.fern.md) |
| `std/tls/server` | [TLS 1.3 server sessions](../internal/stdlib/std/tls/server.fern.md) |
| `std/tls/verify` | [Certificate and signature verification](../internal/stdlib/std/tls/verify.fern.md) |
| `std/tls/x509` | [X.509 certificate fields](../internal/stdlib/std/tls/x509.fern.md) |
| `std/tz` | [Local time-zone rules](../internal/stdlib/std/tz.fern.md) |
| `std/u32` | [Unsigned 32-bit integer methods](../internal/stdlib/std/u32.fern.md) |
| `std/u64` | [Unsigned 64-bit integer methods](../internal/stdlib/std/u64.fern.md) |
| `std/url` | [URL text and query values](../internal/stdlib/std/url.fern.md) |
| `std/utf8` | [UTF-8 code points and bytes](../internal/stdlib/std/utf8.fern.md) |
| `std/uuid` | [UUID values and text](../internal/stdlib/std/uuid.fern.md) |
| `std/wasi_http` | [WASI HTTP host interfaces](../internal/stdlib/std/wasi_http.fern.md) |
| `std/wasm/component` | [WebAssembly component binaries](../internal/stdlib/std/wasm/component.fern.md) |
| `std/wasm/convert` | [WebAssembly conversion opcodes](../internal/stdlib/std/wasm/convert.fern.md) |
| `std/wasm/encode` | [WebAssembly binary containers](../internal/stdlib/std/wasm/encode.fern.md) |
| `std/wasm/imports` | [WebAssembly imports and globals](../internal/stdlib/std/wasm/imports.fern.md) |
| `std/wasm/inst` | [WebAssembly control and value instructions](../internal/stdlib/std/wasm/inst.fern.md) |
| `std/wasm/leb128` | [WebAssembly variable-length integers](../internal/stdlib/std/wasm/leb128.fern.md) |
| `std/wasm/memory` | [WebAssembly memory instructions](../internal/stdlib/std/wasm/memory.fern.md) |
| `std/wasm/module` | [WebAssembly module assembly](../internal/stdlib/std/wasm/module.fern.md) |
| `std/wasm/numeric` | [WebAssembly numeric opcodes](../internal/stdlib/std/wasm/numeric.fern.md) |
| `std/wasm/sections` | [WebAssembly section payloads](../internal/stdlib/std/wasm/sections.fern.md) |
| `std/zstd` | [Zstandard data without dictionaries](../internal/stdlib/std/zstd.fern.md) |
