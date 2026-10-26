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
