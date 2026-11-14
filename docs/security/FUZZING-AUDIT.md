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
| `FuzzSelfHostManifest` | the reader of `fern -manifest` reads a native image | `compiler/drivers/manifest_fuzz_run.fern manifest` | `manifest N` and N fields in printable ASCII with exit 0, the first one the target |
| `FuzzSelfHostIRVerify` | the structural, stack and reference-count verifiers read an op stream | `compiler/drivers/irverify_fuzz_run.fern verify` | a problem count from each verifier, exit 0 |
| `FuzzSelfHostX86Assembler` | the x86-64 assembler reads assembly text | `compiler/drivers/asm_fuzz_run.fern x86` | a summary with exit 0, or an abort with a `fern:` cause |
| `FuzzSelfHostArm64Assembler` | the arm64 assembler reads assembly text | `compiler/drivers/asm_fuzz_run.fern arm64` | the same |
| `FuzzSelfHostX86Image` | the x86-64 assembler, the linker and the ELF writer take assembly text to a static, a `-pie` and a `-g` image | `compiler/drivers/asm_fuzz_run.fern x86-image` | the sizes with exit 0 and a `fern:` cause for each refused image, or an abort with a cause. Each image must parse with Go's `debug/elf`, keep its segments inside the file, agree in address and offset modulo the page, have no segment both writable and executable, and enter in the code |
| `FuzzSelfHostArm64Image` | the arm64 assembler, the linker and the ELF writer, the same way | `compiler/drivers/asm_fuzz_run.fern arm64-image` | the same, with the 64 KiB page of arm64 |
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
| `FuzzSelfHostStdUTF8` | the validator and the lossy decoder of `std/utf8` | `tests/fuzz/stdparse.fern utf8` | a summary with exit 0 that agrees with Go's `unicode/utf8`: the verdict, the offset of the first invalid sequence, and the byte and rune counts |
| `FuzzSelfHostStdH2` | the HTTP/2 frame reader of `std/http/h2/frame`, and a server connection of `std/http/h2` fed the client preface and the input in pieces of nine bytes | `tests/fuzz/stdparse.fern h2` | the same |
| `FuzzSelfHostStdQPACK` | the QPACK decoder of `std/http/qpack`: encoder-stream bytes, then one header section | `tests/fuzz/stdparse.fern qpack` | the same |
| `FuzzSelfHostStdQUIC` | the QUIC packet reader of `std/quic/packet`: the coalesced packets of a datagram, and the opening of each Initial packet with the client Initial keys | `tests/fuzz/stdparse.fern quic` | the same |
| `FuzzSelfHostStdQUICFrames` | the QUIC frame reader of `std/quic/frames` on one packet payload | `tests/fuzz/stdparse.fern frames` | the same |
| `FuzzSelfHostStdTLS` | the TLS record deframer of `std/tls/record`, the handshake message reader of `std/tls/message` on the handshake records, and the extension readers on each hello | `tests/fuzz/stdparse.fern tls` | the same |
| `FuzzSelfHostStdTLSServer` | a server handshake of `std/tls/handshake` fed the input in pieces of eleven bytes | `tests/fuzz/stdparse.fern tls-server` | the same |
| `FuzzSelfHostStdTLSClient` | a client handshake of `std/tls/handshake` fed the input as the server flight, in pieces of eleven bytes | `tests/fuzz/stdparse.fern tls-client` | the same |
| `FuzzSelfHostStdX509` | the PEM reader of `std/tls/pem`, the certificate reader of `std/tls/x509`, and the chain verifier of `std/tls/verify` with each certificate as its own root | `tests/fuzz/stdparse.fern x509` | the same |
| `FuzzSelfHostStdInflate` | the raw DEFLATE decoder of `std/deflate`, compared with Go's `compress/flate` | `tests/fuzz/stdparse.fern inflate` | the same, and agreement with Go |
| `FuzzSelfHostStdGunzip` | the gzip decoder of `std/deflate`, compared with Go's `compress/gzip` | `tests/fuzz/stdparse.fern gunzip` | the same, and agreement with Go |
| `FuzzSelfHostStdZlib` | the zlib decoder of `std/deflate`, compared with Go's `compress/zlib` | `tests/fuzz/stdparse.fern zlib` | the same, and agreement with Go |
| `FuzzSelfHostStdZstd` | the decoder of `std/zstd`, and a round trip of the input through its compressor | `tests/fuzz/stdparse.fern zstd` | the same, and a round trip that gives the input back |
| `FuzzSelfHostStdBrotli` | the decoder of `std/brotli`, and a round trip of the input through its compressor | `tests/fuzz/stdparse.fern brotli` | the same, and a round trip that gives the input back |
| `FuzzSelfHostStdDNS` | the DNS message reader of `std/dns`, and an encode and a second decode of each message it reads | `tests/fuzz/stdparse.fern dns` | the same, and a second decode that gives the same message |
| `FuzzSelfHostStdProtobuf` | the protobuf reader of `std/protobuf`, and the reader again on each length-delimited field | `tests/fuzz/stdparse.fern protobuf` | the same |
| `FuzzSelfHostStdJPEG` | the JPEG decoder of `std/jpeg` under a limit of 1048576 pixels, compared with Go's `image/jpeg` | `tests/fuzz/stdparse.fern jpeg` | the same, and the dimensions that Go reads when both accept the file |
| `FuzzSelfHostStdELF` | the ELF64 reader of `std/elf`: the header, the program headers and the bytes of each segment | `tests/fuzz/stdparse.fern elf` | the same, and the summary that a second reader of the format rules gives, written in Go |
| `FuzzSelfHostStdCpio` | the cpio reader of `std/cpio`, and a read of each entry, a lookup by its name and a list of its children | `tests/fuzz/stdparse.fern cpio` | the same |
| `FuzzSelfHostStdClassFile` | the class file reader of `std/classfile`, and the descriptor readers on each field and method | `tests/fuzz/stdparse.fern classfile` | the same |
| `FuzzSelfHostStdFdt` | the device tree reader of `std/fdt`, and each read of each property of each node | `tests/fuzz/stdparse.fern fdt` | the same |
| `FuzzSelfHostStdTrueType` | the font reader of `std/truetype`: the glyph of seven characters, the metrics, a bitmap of each glyph, and a variable axis | `tests/fuzz/stdparse.fern truetype` | the same |
| `FuzzSelfHostStdTZif` | the TZif reader of `std/tz`, and the offset and the abbreviation of the zone at each transition and at times across the range of an `i64` | `tests/fuzz/stdparse.fern tzif` | the same |
| `FuzzSelfHostStdCSV` | the CSV reader of `std/csv`, and the writer and a second read of each document that it accepts | `tests/fuzz/stdparse.fern csv` | the same, and the same rows from the second read |

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

The TLS seeds come from Go's `crypto/tls` and `crypto/x509`, so the Fern readers start from bytes that a different implementation wrote.
The test runs a Go client against a Go server over loopback TCP and records the ClientHello and the reply of the server.
The certificate seeds are self-signed certificates for `example.com` with ECDSA P-256, Ed25519 and RSA 2048 keys, and a PEM bundle of the three.

`TestSelfHostStdTLSAgainstGo` runs the readers on those bytes and checks the result.
Each Go certificate must parse and verify as its own root.
An expired certificate and a certificate for a different name must parse, but they must not verify.
The Go ClientHello must decode with no refused extension, and a server handshake must reach its request for a signature.

A client handshake must refuse the reply that Go's server wrote for a different client.
The test also sends the ClientHello of the Fern client to a Go server.
The client must read the reply of that server to the end, verify the chain and connect.

The server reader answers a request for a signature with zeros, so a hostile input can reach the next state.

Other TLS seeds carry a ClientHello for TLS 1.2 and a ChangeCipherSpec record before the ClientHello.
They also carry a message length past its record, a record over the size limit and an unknown content type.
The client seeds start from the reply of a Go server to the ClientHello of the Fern client.
The keys of that reply agree, so the flight decrypts.
They also carry a HelloRetryRequest from a Go server that accepts only P-256.

The certificate seeds carry a name of 250 bytes, a truncated certificate and a byte after the certificate.
They also carry PEM text that is not base64 and a DER length of four bytes.

The decoder targets have an oracle as well as the budgets.
Each decoder has an output limit of 1 MiB.
The DEFLATE, gzip and zlib targets decode every input with Go's `compress` packages too.
When both accept the input, the length and the CRC-32 of the two outputs must be the same.
When one accepts the input, the other must accept it too, unless the Go output passes the limit.

The gzip oracle refuses a member header with a reserved flag bit, as RFC 1952 requires and Go's `compress/gzip` does not.
Go reads the name and the comment of a header into a buffer of 512 bytes, and RFC 1952 sets no limit. So the oracle does not judge a member with a longer name or comment.

Go has no zstd or Brotli decoder, so those targets compress each input with the Fern compressor and decode it back.

The DEFLATE, gzip and zlib seeds are text, random bytes, runs and an empty input, written by Go at four levels.
Each seed also has three damaged copies: the last byte flipped, the stream cut in half, and a byte after the end.
Other seeds carry stored blocks with a good and a bad length check, two gzip members, a zlib preset dictionary and bad headers.
The zstd and Brotli seeds are the frames of the codec tests, their damaged copies, and plain inputs for the round trip.

The DNS seeds are queries and replies with compressed names, A, AAAA, CNAME, PTR, SOA and OPT records, and a pointer that points forward.
They also carry a pointer past the message, a label of 64 bytes and more questions than the message holds.
One more seed carries a record shorter than its length.
The protobuf seeds carry each wire type, nested messages, a varint at its limit and a length past the input.
They also carry the group wire types, an unknown wire type and a field number of 0.

The JPEG seeds are files that Go's encoder wrote at three qualities, the Pillow files of `tests/stdlib/image_vectors.fern` and damaged copies.
Other seeds carry a frame of 65535 by 65535 pixels, a lossless frame and a file with no frame.
`TestSelfHostStdJPEGAgainstGo` requires the dimensions that Go reads for each Go and Pillow file.

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

The image targets ran 45 seconds each on 2026-11-09, after their seeds passed and after the four faults below were fixed.
Host: Linux 6.18.44-fc-v310, Intel Xeon 2.10GHz, 4 cores, Go go1.27.3.

| Target | Executions | New inputs kept | Findings |
|---|---|---|---|
| `FuzzSelfHostX86Image` | 42302 | 10 | none after the fixes |
| `FuzzSelfHostArm64Image` | 39511 | 2 | none after the fixes |

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
| `FuzzSelfHostStdUTF8` | 66151 | 43 | none |
| `FuzzSelfHostStdH2` | 47447 | 1 | none |
| `FuzzSelfHostStdQPACK` | 46110 | 1 | none |
| `FuzzSelfHostStdQUIC` | 65431 | 4 | none |
| `FuzzSelfHostStdQUICFrames` | 57205 | 1 | none |
| `FuzzSelfHostStdTLS` | 45705 | 0 | none |
| `FuzzSelfHostStdTLSServer` | 48819 | 3 | none |
| `FuzzSelfHostStdTLSClient` | 47367 | 4 | none |
| `FuzzSelfHostStdX509` | 57720 | 0 | none |
| `FuzzSelfHostStdInflate` | 48612 | 23 | none |
| `FuzzSelfHostStdGunzip` | 30298 | 11 | none |
| `FuzzSelfHostStdZlib` | 17732 | 15 | none |
| `FuzzSelfHostStdZstd` | 23013 | 1 | none |
| `FuzzSelfHostStdBrotli` | 43762 | 2 | none |
| `FuzzSelfHostStdDNS` | 57088 | 2 | none |
| `FuzzSelfHostStdProtobuf` | 53336 | 1 | none |
| `FuzzSelfHostStdJPEG` | 10861 | 1 | three, fixed in this change |
| `FuzzSelfHostStdELF` | 5382 in 180 s (2026-11-14) | 5 | none by the target. One by the review of the loader, fixed in the same change |
| `FuzzSelfHostStdCpio` | 17466 in 120 s (2026-11-14) | 1 | none |
| `FuzzSelfHostStdClassFile` | 16916 in 120 s (2026-11-14) | 1 | none |
| `FuzzSelfHostStdFdt` | 17377 in 120 s (2026-11-14) | 1 | none |
| `FuzzSelfHostStdTrueType` | 31047 in 150 s (2026-11-14), on the repairs of main | 1 | four, which `31960636` on main repairs |
| `FuzzSelfHostStdTZif` | 18543 in 120 s (2026-11-14) | 1 | none |
| `FuzzSelfHostStdCSV` | 25172 in 150 s (2026-11-14), after the repair | 1 | one, fixed in this change |

An abort with a cause is an accepted outcome for the assemblers. They consume the compiler's own output, and a bounds abort on foreign text is the language's defined trap.
The run logs are in the pull request.

## Findings

The TrueType target found four faults of the font reader: a bounds trap in the reader of a GPOS feature list, a bitmap of 1.5 GB for a font with 1 unit to the em, contour end points that go down, and a composite glyph with many parts at each of its 8 levels.
Commit `31960636` on main found the same four faults in a review of the module, and its repairs are the ones in the tree. `kern_lookups` reads through the checked byte reader. `render` gives an empty bitmap for a box above 16777216 pixels. `simple` gives no outline for end points that do not rise. One glyph reads 4096 glyphs at the most.
The target confirms those repairs. Its two inputs are in `testdata/fuzz/FuzzSelfHostStdTrueType/`: the second one took 1491020 KB before the repairs, and it takes 13032 KB after them. `tests/stdlib/truetype_test.fern` has a font with a feature count of 65535 and a font with 1 unit to the em.

The CSV target found one document that changes after a write and a second read: one carriage return.
`parse` gives one record of one empty field for it. `serialize` wrote that record as an empty line, and `parse` takes an empty line at the end of a document for no record.
So the last record of a document was lost when it had one empty field. `serialize` now writes such a record as a pair of quotes. `tests/stdlib/csv_test.fern` has the case, and the input is in `testdata/fuzz/FuzzSelfHostStdCSV/`.

The review for the ELF target found one fault in `std/user_process`, the one program that loads an image through `std/elf`.
A load segment with more bytes in the file than in memory passed the range check of the loader, which uses the memory size. The loader then mapped and wrote the pages of the file bytes beyond that range.
`Segment.bytes` now refuses such a load segment, so the loader refuses the image. `tests/stdlib/elf_test.fern` has the case, and the target has three seeds for it.

The JPEG target found three ways that a hostile file stopped the decoder with a bounds trap.
Each is fixed in `std/jpeg`, and each has a test in `tests/stdlib/jpeg_test.fern` and a seed in `testdata/fuzz/FuzzSelfHostStdJPEG/`.

- A frame of 65535 by 65535 pixels. The block counts of the frame passed the range of an i32, so the coefficient buffer was too small. The decoder now counts the coefficients as i64 and refuses a frame of more pixels than its limit with `Unsupported`, before it allocates. `decode_image_limit` takes the limit.
- A segment shorter than its fields. A frame of length 0 read its fields past the end of the file. Each segment now checks its length: the frame, the quantization tables, the restart interval and the scan header. A second frame is `Corrupt`, because the coefficients were sized for the first.
- A scan that uses a Huffman table that the file does not define. The decoder read the empty table. It now decodes such a scan as corrupt data, as the module contract says for other corrupt data.

The image targets found four faults in the last step of a native compile.
They found three on their seeds and one in their first 6 seconds of fuzzing.
Each is fixed in `compiler/elfimage.fern` or `compiler/elf.fern`, and `TestSelfHostImageOfCompiledProgram` or a layout test checks it.

- The relro step stopped the compiler with exit 3 when a relocating image had no relro symbols. Text compiled without `-pie` and linked as a `-pie` image reached it. The step now refuses the image through the linker, with the same cause line, as the linker refuses its other faults.
- An arm64 image of text with no `_start` took the start of the code as its entry. With no code, the entry was past the code. The arm64 linker now refuses such an image, as the x86-64 linker does.
- On both ISAs, an image of text with a `_start` label and no instruction after it entered at the end of the code. The minimised input is `.text\n_start:`. Both linkers now refuse an image with no code at `_start`, and `testdata/fuzz/FuzzSelfHostX86Image/` and `testdata/fuzz/FuzzSelfHostArm64Image/` keep the inputs.
- The `PT_GNU_RELRO` header of a `-pie` executable and of a shared object gave its memory size as its file size. Its file range then ended past the end of the file. The file size is now the part of the range that the file holds. `TestSelfHostImagePermissions` and `TestSharedObjectLayout` check that each segment lies inside the file.

## Coverage feedback for the readers

Go's fuzzer steers by the coverage of the Go test binary.
It cannot see into the Fern process that reads each input, so it mutates the readers blind.
`TestSelfHostReaderCoverageGuidedFuzz` in `internal/testing/e2ecompiler/self_host_fuzz_cover_test.go` steers them by the coverage of that process.
It builds the program of each reader with `fern -cover`, and each run writes its counters to stderr at exit.
The loop keeps each input that reaches a counter that no earlier input reached.

The loop takes forty readers: the twenty-eight of `tests/fuzz/stdparse.fern`, and the readers of five drivers in `compiler/drivers/`.
The drivers read build manifests, packages, literate documents, op streams and assembly text.
The image modes of `asm_fuzz_run.fern` write their images beside the input, so the check can read them.
The counters of a program cover the modules that it imports.

| Program | Readers | Counters |
|---|---|---|
| `tests/fuzz/stdparse.fern` | 21 | 56,362 |
| `compiler/drivers/manifest_fuzz_run.fern` | 1 | 10,080 |
| `compiler/drivers/pkg_fuzz_run.fern` | 4 | 16,969 |
| `compiler/drivers/literate_fuzz_run.fern` | 2 | 13,581 |
| `compiler/drivers/irverify_fuzz_run.fern` | 1 | 15,646 |
| `compiler/drivers/asm_fuzz_run.fern` | 4 | 32,837 |

`readerTargets` holds the program, the seeds and the check of each reader.
The Go target and the loop use the same seeds, the same budgets and the same oracle.
The loop writes a failing input to `testdata/fuzz/<Target>/` in the format of Go's fuzzer, so the Go target replays it on each pull request.
`FERN_READER_COVER_FUZZ_TIME` is the budget of each reader, and `FERN_READER_COVER_FUZZ_CORPUS` keeps the corpus of each reader between runs.

```sh
FERN_READER_COVER_FUZZ=1 FERN_READER_COVER_FUZZ_TIME=5m go test -run '^TestSelfHostReaderCoverageGuidedFuzz$/^FuzzSelfHostStdJSON$' ./internal/testing/e2ecompiler
```

`TestSelfHostReaderCoverageBeatsBlind` runs the loop twice from the seeds `""` and `"0"`, with the same random streams.
One arm keeps each input that reaches a new counter, and the other arm keeps none.
Summed over two streams of 600 runs, the arm with feedback must reach more counters.
From the readers' own seeds, 300 runs of each arm reached the same counters to within 4%, so the test uses the almost empty seeds.

| Reader | Program | Feedback, counters | Blind, counters |
|---|---|---|---|
| DEFLATE | `stdparse.fern` | 1201 | 823 |
| URL | `stdparse.fern` | 1537 | 1324 |
| protobuf | `stdparse.fern` | 536 | 509 |
| index | `pkg_fuzz_run.fern` | 524 | 427 |
| tangle | `literate_fuzz_run.fern` | 498 | 399 |
| verify | `irverify_fuzz_run.fern` | 2373 | 2129 |
| x86 | `asm_fuzz_run.fern` | 1789 | 1703 |

A run of `stdparse.fern` costs about 45 ms, and most of that time is the report.
A run of a driver costs about 15 ms.
An abort ends a program before it writes its report, so an aborted run of an assembler adds no counters.
In the CI run of 2026-11-09, each of the other thirty-two readers kept new inputs in 5 seconds from its seeds.
No run found a fault.
The `utf8` reader kept 12 new inputs in 20 seconds on the host.

## Crash triage

`go test -fuzz` minimises a failing input and writes it under `internal/testing/e2ecompiler/testdata/fuzz/<Target>/`.
Commit that file with the fix, so the per-PR run replays it.
The failure message names the budget that broke, the signal, or the exit status and the output.
A hang names the budget, and a budget breach names the resident size.

## In CI

`fuzz-diff.yml` runs the forty targets for 30 seconds each on the x86-64 runner, from the committed seeds, after the differential targets.
The drivers run natively, so the arm64 runner does not run them.
The `fuzz` job of `nightly-fuzz.yml` runs each of the forty targets on the x86-64 runner for 20 minutes.
Each target starts from a cached corpus that grows from night to night.
`fuzz-diff.yml` also runs the coverage-guided loop of the forty readers for 5 seconds each, after `TestSelfHostReaderCoverageBeatsBlind`.
Each nightly row of a reader then runs that loop for 15 minutes from its own cached corpus.

## Limits

- The manifest target reads through `compiler/imagemanifest.fern`, which `fern -manifest` also calls. `TestSelfHostManifestRefusal` and `TestSelfHostProfiles` test the CLI around that reader.
- The memory budget is a resident-size ceiling on the child, not a per-allocation count.
- The compiler reads no object file or archive from outside. So the image targets reach the linker and the ELF writer only through the assemblers. A fault that the assemblers refuse before the link stays out of reach.
