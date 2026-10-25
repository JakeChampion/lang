# Lossless numeric conversions

Issue: #12696.
Core commit: `c89b24f6f6c8e5abd20b7c4e014ca4b11900ac0c`.
Base commit: `ee72b0f3bb6d26789191fac200234bf210f9e03d`.

The change permits scalar conversions that preserve every source value.
Both compilers emit conversions before storage or an operation.
An expression retains its source arithmetic before its result converts.
Stored containers retain their element types.
An unsuffixed literal still takes the type that its context requires.

## Source and producers

The compiler and standard library source digest is
`789de058835e7c3c86a587a035b98b073b7601fbf76a3dae9cfc302b974824ef`.
The digest includes each tracked path and its contents, separated by zero bytes.

The bootstrap starts from the exact stage0 pin in this base commit:
`80667735ba7936ace5f1925ec03bcdde5a1bfe36aaa3c9f7e080fdecdcaf24ee`.
All `FERN_*` variables are removed for the bootstrap and driver builds.
The bootstrap metadata names the committed source.

Stage 2 and stage 3 are identical at 14,256,640 bytes.
Their SHA-256 is
`fe9894b204fba76c8614b1eceb35ae2f838718c175911ae5755363cc349c2f70`.
The manifest records every producer and output digest.

## Checks


- All 11 strict binary-size gates pass against the unchanged baselines.
  The measured increases are 1.3% to 2.3%, within the 5% limit.

- The runtime and formatter matrix passes all 150 commands.
  It covers both interpreters, both formatters, and x86-64, ARM64, and WASI binaries.
  Every compiled run enables strict IR, sanitizer checks, and leak checks.
  The new runtime case records five allocations, five frees, and zero live bytes on each target.

- Each compiler passes 43 positive and negative checker probes.
  The negative probes include narrower destinations and stored containers.

- The numeric property suites pass 765 backend subtests in 268.403 seconds.
  These include 180 random-seed legs, 504 exact float legs, and 81 regression legs.
  Each suite runs on x86-64, ARM64, and WASI without skips.

- The fixture suites pass 42 selected cases across seven runners in 77.694 seconds.
  These include the shipped CLI, both interpreters, all three targets, and x86-64 quarantine.
  The fixture format check also passes.

- The complete checker, parser, and source-lint packages pass.
  `go vet` passes for the checker package.
  The formatter accepts all 1,178 tracked Fern source files in the standard format gate.

- The prose check reports zero errors and zero warnings.
  The comment check reports zero errors and eight word-usage warnings.
  Its raw output is included for review.

## Records

`manifest.json` records file lengths and SHA-256 digests.
Large records use gzip without a timestamp.
`core-source.patch.gz` contains the exact base-to-core change.
Its reverse application was checked against the committed tree.

The matrix records every command, exit code, output, and diagnostic.
The checker probe records include every accepted or rejected source case.
The Go logs retain the individual fixture and numeric property results.
The size report includes every driver from the strict gate.
