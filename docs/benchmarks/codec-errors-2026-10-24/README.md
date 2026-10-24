# Checked codecs, SemVer, and UUID validation

Status: [record]

The hexadecimal, Base32, and Base64 decoders return typed errors.
The errors identify invalid bytes, lengths, padding, and nonzero trailing bits.
SemVer and UUID operations return typed parse errors with byte offsets.
All new error types implement the shared error and display traits.

The encoders accept byte views.
Hash updates and other byte consumers use the same borrowed byte representation.
The source patch includes the caller migrations and removes the old codec and UUID names.

## Source and producers

The core source commit is `2619689147c678591dbb1d0711321f76e3133c71`.
The base commit is `46c549ab2b755c973371cda995a1e14b684dedbc`.
The compiler and library source match commit `8664c4e9a72756af1bd3ea3847b6f3774bd19765`.
The last core commit changes two negative test inputs only.

[artifacts.json](artifacts.json) records the source snapshot for each artifact.
[source-files.json](source-files.json) records 496 compiler and library file hashes.
[change.patch.gz](change.patch.gz) contains the complete change from the base.
The patch passes a reverse-application check against the core source.

The pinned compiler builds three compiler stages.
Stage 2 and stage 3 match byte for byte.
Their SHA-256 is `0fe9ce63bf01825493baf2c3c2299a8b0a908fde79cb206cbf9a69c06be6d5f4`.
The complete strict size check passes all 11 unchanged driver limits.

## Checks

The final matrix passes 131 commands across 13 programs.
Both checkers check every program.
Both interpreters execute the seven codec and parse programs.
All programs execute on x86-64 Linux, ARM64 Linux, and WebAssembly.
All programs also cross-compile for ARM64 Darwin.
Darwin execution requires the macOS CI runner.

The 39 compiled executions use strict IR, sanitizer checks, and leak checks.
Every execution reports balanced allocations and zero live bytes.
The programs include codec errors, SemVer, UUID, ECDSA, RSA, AEADs, DEFLATE, and Brotli.
Their TAP output or expected exit agrees across execution targets.

The byte API, checksum, reduction, and formatter regression set passes 28 tests without skips.
The compiler source, error contracts, capability classifications, documentation metadata, and conformance census checks also pass.
Formatting checks pass for 1,200 Fern source files and all Go files.
Go vet passes.

The program corpus check passes all 526 programs at commit `c5fb9cc02`.
The artifact retains that snapshot identifier.
The final matrix and source checks cover the subsequent compiler and display changes.

## Error regressions

The baseline accepts the noncanonical Base32 input `MZ======` and Base64 input `Zh==`.
The checked decoders reject both at byte offset 1.
The baseline SemVer parser accepts invalid identifier characters and empty build identifiers.
The checked parser rejects those inputs and retains valid version precedence.

The compiler prerequisite fixes method result freshness by receiver identity.
An unrelated method with the same name cannot change the ownership contract of a consuming chain.
The compiler regression set also checks that borrowed results remain invalid in consuming positions.
