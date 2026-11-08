# Fuzzing the compiler's later boundaries

Status: [reference] audit and targets for [SEC-040](https://github.com/JakeChampion/lang/issues/13542).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The generators, the differential oracles and the mutation score are in `docs/FUZZING.md`.

## What existed

`FuzzParse` and `FuzzCheck` drive the Go parser and checker in process, with committed corpora.
`FuzzSelfHostFrontEnd` drives `fern -check` on hostile source and must see a diagnostic, never a signal or a hang.
`FuzzGenerate_ExecutionAgrees`, `FuzzGenerate_StdoutAgrees` and `FuzzGenerate_ParseRoundTrips` drive fernsmith programs through the oracles.
Nothing fed hostile bytes to the boundaries after the front end: the IR verifiers, the in-process assemblers, and the readers of a native image.

## The new targets

| Target | Boundary | Driver | Accepted outcomes |
|---|---|---|---|
| `FuzzSelfHostManifest` | `fern -manifest` reads a native image | the CLI | the fields in printable ASCII with exit 0, or `fern: no build manifest in` with exit 1 |
| `FuzzSelfHostIRVerify` | the structural, stack and reference-count verifiers read an op stream | `compiler/drivers/irverify_fuzz_run.fern` | a problem count from each verifier, exit 0 |
| `FuzzSelfHostX86Assembler` | the x86-64 assembler reads assembly text | `compiler/drivers/asm_fuzz_run.fern x86` | a summary with exit 0, or an abort with a `fern:` cause |
| `FuzzSelfHostArm64Assembler` | the arm64 assembler reads assembly text | `compiler/drivers/asm_fuzz_run.fern arm64` | the same |
| `FuzzSelfHostToml` | the manifest reader and its diagnostic formatter read a `fern.toml` | `compiler/drivers/pkg_fuzz_run.fern toml` | a summary with exit 0 |
| `FuzzSelfHostLock` | the lock reader reads a `fern.lock` | `compiler/drivers/pkg_fuzz_run.fern lock` | the same |
| `FuzzSelfHostIndex` | the index reader reads a registry `index.toml` | `compiler/drivers/pkg_fuzz_run.fern index` | the same |
| `FuzzSelfHostArchive` | the unpacker reads a package archive: the gunzip, the tar reader and the entry name rules | `compiler/drivers/pkg_fuzz_run.fern archive` | the same |
| `FuzzSelfHostTangle` | the chunk grammar, the tangle of the root chunk and of the file roots, and the unused-chunk lint read a `.fern.md` document | `compiler/drivers/literate_fuzz_run.fern tangle` | the same |
| `FuzzSelfHostWeave` | the weave reads a `.fern.md` document and writes text and HTML | `compiler/drivers/literate_fuzz_run.fern weave` | the same |
| `FuzzSelfHostStdHTTP` | the HTTP/1 request framer of `std/http`, and its chunked-body decoder fed in pieces of seven bytes | `tests/fuzz/stdparse.fern http` | the same |
| `FuzzSelfHostStdHPACK` | the HPACK decoder of `std/http/hpack`, two blocks on one decoder | `tests/fuzz/stdparse.fern hpack` | the same |
| `FuzzSelfHostStdJSON` | the JSON parser of `std/json` | `tests/fuzz/stdparse.fern json` | the same |
| `FuzzSelfHostStdURL` | the URL parser and the query parser of `std/url` | `tests/fuzz/stdparse.fern url` | the same |
| `FuzzSelfHostStdH2` | the HTTP/2 frame reader of `std/http/h2/frame`, and a server connection of `std/http/h2` fed the client preface and the input in pieces of nine bytes | `tests/fuzz/stdparse.fern h2` | the same |
| `FuzzSelfHostStdQPACK` | the QPACK decoder of `std/http/qpack`: encoder-stream bytes, then one header section | `tests/fuzz/stdparse.fern qpack` | the same |
| `FuzzSelfHostStdQUIC` | the QUIC packet reader of `std/quic/packet`: the coalesced packets of a datagram, and the opening of each Initial packet with the client Initial keys | `tests/fuzz/stdparse.fern quic` | the same |
| `FuzzSelfHostStdQUICFrames` | the QUIC frame reader of `std/quic/frames` on one packet payload | `tests/fuzz/stdparse.fern frames` | the same |

The verifier driver decodes the input bytes into ops.
The first two bytes choose the parameter and local counts.
Every later group of four bytes is one op: a kind up to 511, a small signed immediate, a width with its sign flag, and a callee name with a declaration index.
Unknown kinds and unknown callees are part of the input space, because the verifiers must refuse them.

The package readers take the bytes an attacker controls when a program depends on a package: the manifest and lock of a dependency, the index a manifest names, and the archive the index points at.
Each reader refuses a malformed input with a message, so the one accepted outcome is exit 0 with the reader's summary.
The archive target unpacks into a directory of its own under the input's directory. Its seeds carry an entry that escapes the package directory, a long name and a truncated archive.

A package can ship `.fern.md` modules, so the literate reader takes attacker bytes too.
The tangle target runs four readers over one document and prints their counts.
They are the chunk grammar, the tangle of the root chunk, the tangle of the file roots and the unused-chunk lint.
The weave target writes the document as text and as HTML.

The seeds carry an undefined reference, two chunks that reference each other, and a chunk that references itself.
They also carry an escaped reference, an unterminated fence, an empty chunk name and a `file=` path that leaves the package directory.
Two more hold bytes that are not UTF-8 and a root chunk of three thousand references.

The stdlib targets feed the parsers that a server runs on the bytes of its peers.
The shipped CLI builds `tests/fuzz/stdparse.fern`, so they fuzz the standard library as this tree compiles it.
The pinned stage0 cannot build that program, because `std/http` reaches `std/http/qpack` through `std/platform` and `std/fetch`.
The HPACK target decodes the input as two blocks on one decoder, so the second block reads the dynamic table that the first block filled.
The HTTP seeds carry a request with both a length and a chunked body, two length fields and a negative length.
They also carry a chunk size of sixteen hex digits, a folded header line and a NUL in a header value.

The HTTP/2 seeds carry a HEADERS frame with its CONTINUATION, and a CONTINUATION with no HEADERS before it.
They also carry a zero window increment, DATA on stream 0, a pad longer than its frame and a stream that depends on itself.
One seed opens and resets a thousand streams at once.
The first byte of a QPACK input gives the length of the encoder-stream bytes, and the rest is one header section on stream 0.

The QUIC seeds start from a client Initial packet that the test seals from RFC 9001 with Go's own HKDF, AES-GCM and AES.
The packet reader opens that packet, which checks the two implementations against each other.
Other seeds carry two coalesced Initial packets, a flipped tag byte and a truncated packet.
They also carry a version negotiation packet, a Retry packet, a connection ID of 21 bytes and a length past the datagram.

The frame seeds carry ACK frames with ranges and with an ECN count, and CRYPTO and STREAM frames.
They also carry a stream offset at the varint limit and a connection ID of 21 bytes.
Two more carry a close reason that is not UTF-8 and an unknown frame type.

Every input runs in a child process under two budgets, 20 seconds of wall time and 512 MiB of resident memory.
A hang, a signal or a budget breach fails the input whatever the exit status.
An input over 64 KiB is skipped.
The seeds are in each target's `f.Add` list. They include a window of a real image around its manifest, a manifest line without its newline, and the magic repeated. They also include the compiler's own emitted text for a small program, malformed directives, and empty input.

## Results on this host

Host: Linux 6.18.44-fc-v165, Intel(R) Xeon(R) Processor @ 2.80GHz, Go go1.27.3.
Each target ran 45 seconds after its seeds passed.

| Target | Executions | New inputs kept | Findings |
|---|---|---|---|
| `FuzzSelfHostManifest` | 33105 | 2 | none |
| `FuzzSelfHostIRVerify` | 22414 | 0 | none |
| `FuzzSelfHostX86Assembler` | 18168 | 2 | none |
| `FuzzSelfHostArm64Assembler` | 39065 | 3 | none |

The package readers ran 45 seconds each on 2026-11-08 on the same kind of host, after their seeds passed.

| Target | Executions | New inputs kept | Findings |
|---|---|---|---|
| `FuzzSelfHostToml` | 33497 | 1 | none |
| `FuzzSelfHostLock` | 31774 | 1 | none |
| `FuzzSelfHostIndex` | 31382 | 0 | none |
| `FuzzSelfHostArchive` | 56102 | 2 | none |

The literate readers ran 45 seconds each on 2026-11-08 on the same kind of host, after their seeds passed.

| Target | Executions | New inputs kept | Findings |
|---|---|---|---|
| `FuzzSelfHostTangle` | 73799 | 0 | none |
| `FuzzSelfHostWeave` | 53269 | 1 | none |

The stdlib parsers ran 45 seconds each on 2026-11-08 on the same kind of host, after their seeds passed.

| Target | Executions | New inputs kept | Findings |
|---|---|---|---|
| `FuzzSelfHostStdHTTP` | 48180 | 2 | none |
| `FuzzSelfHostStdHPACK` | 48783 | 2 | none |
| `FuzzSelfHostStdJSON` | 50608 | 1 | none |
| `FuzzSelfHostStdURL` | 53437 | 1 | none |
| `FuzzSelfHostStdH2` | 47447 | 1 | none |
| `FuzzSelfHostStdQPACK` | 46110 | 1 | none |
| `FuzzSelfHostStdQUIC` | 65431 | 4 | none |
| `FuzzSelfHostStdQUICFrames` | 57205 | 1 | none |

An abort with a cause is an accepted outcome for the assemblers. They consume the compiler's own output, and a bounds abort on foreign text is the language's defined trap.
The run logs are in the pull request.

## Crash triage

`go test -fuzz` minimises a failing input and writes it under `internal/testing/e2ecompiler/testdata/fuzz/<Target>/`.
Commit that file with the fix, so the per-PR run replays it.
The failure message names the budget that broke, the signal, or the exit status and the output.
A hang names the budget, and a budget breach names the resident size.

## In CI

`fuzz-diff.yml` runs the eighteen targets for 30 seconds each on the x86-64 runner, from the committed seeds, after the differential targets.
The drivers run natively, so the arm64 runner does not run them.
The `fuzz` job of `nightly-fuzz.yml` runs each of the eighteen targets on the x86-64 runner for 20 minutes.
Each target starts from a cached corpus that grows from night to night.

## Limits

- No coverage feedback: Go's instrumentation does not see into a Fern process, so the mutations are blind. The IR driver's dense encoding keeps every mutation a valid op stream, which is what makes blind mutation reach the verifiers.
- The memory budget is a resident-size ceiling on the child, not a per-allocation count.
- The ELF writer and the linker take only what the assemblers produce, and no target feeds them foreign input.
