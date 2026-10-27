# Native hardening CLI mode evidence

The source commit is `4f0e7f0a2f4d27f503720cb2dce05d1e722a5a57`.
The clean base is `50e121fbab40c3ffe7f0172cf0ac2f51723e04e4`.
The manifest binds the copied raw records to their original paths and exact hashes.

Clean main reproduced the unchanged flag-refusal failure with two failing nodes and zero skips.
The command requested native executable hardening without explicit shared mode.
The `.so` filename did not change its output mode.
The independent capability probe observed executable ELF type, no writable-executable load segment, a non-executable stack declaration, and actual exit 42.
Explicit shared mode on both native targets and Wasm hardening each returned exit two.
These finite observations establish the current CLI contract, not general security qualification.

The corrected refusal row adds explicit shared mode.
All other original invalid-flag cases retain their required exit codes.
Two additional rows cover ARM64 shared-mode hardening and unsupported Wasm hardening.
The positive control compiles a native executable with the same `.so` suffix and checks its image and actual exit code.
The website table now matches the current CLI help text.

The focused candidate passed three nodes with zero skips before the source commit.
That record retains its uncommitted source status.
The clean committed full command returned Go exit zero with 125 passing nodes, zero failures, and two skips.
Its qualification wrapper returned one because the launch omitted the installed ARM cross-toolchain paths.
That initial command does not count as a complete pass.

The corrected launch exported the existing cross-toolchain and QEMU paths without source changes.
It ran only the two skipped ARM output cases.
That command passed three nodes, including the repeated parent node, with zero skips.
The union contains all 127 original test nodes with no missing case or repeated completed semantic case.
The union record names every node and retains both command results.

All 2,084 format inputs, Go build and vet, workflow checks, and SourceLint passed.
SourceLint reported 219 passing nodes and zero skips.
All guarded sources and the recorded format/probe compiler remained unchanged.
The stock Go harness compiled the CLI from the pinned source through its normal cache.
The record does not claim a separate hash for every private harness executable.

The format and capability compiler came from clean build `3a70b650d36331d2febe0f88d207bf0702e09ef4`.
Its compiler and standard-library sources match this candidate exactly.
This test and documentation change did not require a compiler rebuild.
Every production compiler source, runtime source, standard-library source, baseline, workload, threshold, and refusal policy remains unchanged.
Actual CI recovery, the wider retained-machine proof, and all actual M5 and epic acceptance gates remain open.
