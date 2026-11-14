# ELF input boundaries and output inspection

Status: [reference] partial audit and harness fix for [SEC-026](https://github.com/JakeChampion/lang/issues/13528).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
Source audit: `b2c7c6251e37479b81ba5b50099f087cd4f485f7`, dated 2026-10-26.

## Accepted inputs

The self-host compiler accepts Fern source and package data.
It does not accept external ELF objects, archives, or shared libraries as link inputs.
The native assemblers resolve generated labels and relocations in memory.
The ELF writer then constructs the executable or shared image.
See `compiler/fern.fern`, `compiler/x86_native.fern`, `compiler/arm64_native.fern`, and `compiler/elf.fern`.

The CLI reads source bytes through `read_source_file` and `lexer.decode_source`.
An ELF file supplied as source receives parser diagnostic `P001`.
The compiler does not interpret its program headers, symbols, or relocations.
Opaque embedded asset bytes do not become object files for the linker.
The pinned bootstrap executable forms a separate trusted boundary.
Its hash check does not establish an ELF parser for untrusted link inputs.

Manual probes supplied ELF magic, a complete ELF64 header, a truncated header, and overflowed offsets as source.
Each probe received exit status 1 and `P001` for both Linux target selections.
Invalid UTF-8 and NUL-byte controls also received `P001`.
Valid Fern source passed.
These probes establish source rejection, not ELF metadata validation.

## The ELF64 reader of the standard library

The compiler reads no ELF input, but a Fern program can. `std/elf` reads an ELF64 image: the header, the program headers and the bytes of each segment.
`std/user_process` loads a static program through it, so the image is a file that the loader did not write.

The reader refuses each of these with an error value that has the offset of the fault:

- An image shorter than the header of 64 bytes, or one that is not a little-endian ELF64 image of version 1.
- A program header size that is not 56, when the image has program headers.
- A program header table that is not inside the image. The check has no sum that can wrap: it compares the size of the table with the bytes after its offset.
- A segment whose file range is not inside the image, by the same form of check.
- A load segment with more bytes in the file than in memory. A loader that copies them writes beyond the memory of the segment.

The last rule is new. The loader of `std/user_process` checked the memory range of a segment, and then copied the file bytes.
Such a segment made the loader map and write pages beyond the range that it checked.

`FuzzSelfHostStdELF` feeds hostile bytes to the reader through `tests/fuzz/stdparse.fern elf`.
Its check is a second reader of the same rules, written in Go. The two must give the same result for each input: the offset of the refusal, or the header fields and the length and a sum of the bytes of each segment.
The seeds have each fault of the identification, and a count of 65535 program headers.
They have a table offset and a segment offset of 2^31, of 2^32 and of 2^64 - 1.
They also have sizes that wrap a sum of 64 bits.

The reader does not read section headers, symbols, relocations or notes. It does not check that the entry address is in an executable segment. It does not check that two load segments are apart. A loader has those duties.

## The manifest reader

`fern -manifest FILE` is the one compiler command that reads a binary file.
It reads the whole file and searches the bytes for the manifest magic, `FERNBUILD1 target=`.
It does not parse a header, a section, a symbol or a relocation of an ELF, Mach-O or wasm file.
So a malformed header cannot change what it reads.

A candidate line must end in a newline within 512 bytes of the magic.
It must also name a target, carry the `profile=` and `heap_limit=` fields, and hold printable ASCII only.
The reader skips any other candidate and searches on.
Without an accepted candidate the command exits 1 with `fern: no build manifest in FILE`.

The reader copied the bytes of a candidate to stdout unchecked.
A file with an escape sequence or a NUL byte in its manifest line then wrote those bytes to the terminal.
`FuzzSelfHostManifest` now requires printable ASCII on stdout, and four seeds pin the rule: a NUL, an escape sequence, bytes that are not UTF-8, and a hostile line before a valid one.
Against the previous reader the four seeds failed, and against this one they pass.

## Security harness input

`scripts/security-smoke` reads generated ELF images before it executes the positive controls.
Its property inspector must reject malformed images before it reports W^X or NX success.
The inspector now checks these conditions:

- The file contains a complete little-endian ELF64 executable header for the expected machine.
- The file type is `ET_EXEC` or `ET_DYN`, the version is 1, and the header size is 64.
- The complete program-header table lies inside the file.
- Segment file ranges lie inside the file, and file sizes do not exceed memory sizes.
- Load virtual ranges fit unsigned 64-bit arithmetic.
- Declared load alignment is 0, 1, or a power of two with congruent file and virtual offsets.
- The entry address lies inside file-backed executable memory.
- No load segment permits both writes and execution.
- Writable and executable load segments share no 4 KiB page.
- The stack header prohibits execution.

Empty load segments occupy no pages for the overlap check.
The page check uses the Linux 4 KiB contract of this harness.
It does not measure the physical host page size or establish all kernel loader rules.
The inspector does not parse section headers, symbols, dynamic relocations, or unwind metadata.
It does not certify arbitrary ELF files as safe to execute.

## Evidence and remaining work

The new negative controls reproduced acceptance of invalid alignment, virtual overflow, and invalid entry addresses before the fix.
They also reproduced rejection of an empty load segment that occupies no pages.
The unit suite passes all 18 tests after the fix:

```sh
python3 -m unittest discover -s scripts -p test_security_smoke.py -v
```

The unchanged execution matrix passes all 20 case and target pairs:

```sh
scripts/security-smoke --compiler /workspace/lang-main-ci/bin/fern --report /workspace/sec026-inspection-smoke.json
```

The matrix uses native Linux x86-64 and AArch64 under QEMU.
The compiler SHA-256 is `1903581960ff5fd7a310b071ba89b537f4911a7a71f085d67aa39214641e57be`.
Hardware enforcement remains unverified.
CI results belong in the linked PR before merge.

SEC-026 remains open for its full acceptance review.
External object support must define size limits before it accepts such files.
It must check headers, symbols, relocations, offsets, and alignment.
The output inspector supplies no substitute for those checks.
