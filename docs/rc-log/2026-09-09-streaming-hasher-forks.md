# Streaming hasher snapshots through the self-hosted compiler

Refs #8874 under #8920. These tests exercise the real `std/crypto` callers of
`__copy_in(own dst)`, on top of the scalar-array repair in #8963.

For each of MD5, SHA-1, SHA-224, SHA-256, SHA-384, SHA-512 and BLAKE2b-512:
start a streaming state with `ab`, retain a snapshot, then update the two
states with `c` and `d`. Check both final digests after both writes. The test
table uses independent Python 3.13.3 `hashlib` results for `abc` and `abd`, so two
equally wrong computations cannot satisfy the comparison. The outer frame
also checks reference-count underflows after the state-owning frame exits.

## Reproduction and scope

On native macOS ARM64, a Fern-built stage1 compiler from unchanged base
`7028395a7` compiles all seven fixtures, but every output exits 1: the first
fork's digest is wrong after updating the second fork. The same seven
programs compiled by the Fern-built stage1 from `766e0a663` exit 0. Both
stage1 compilers were built through `STAGE0=<local candidate> make bootstrap`,
starting with an ARM64 Darwin compiler built using Go 1.26.0. This tests
the compiler produced by Fern as well as the normal Go-built self-host driver.

The permanent tests are `TestSelfHostCryptoForkIRX86_64`,
`TestSelfHostCryptoForkIRArm64` and `TestSelfHostCryptoForkIRWasm`. They use
the production CLI's IR-or-error emitters with the real stdlib import.
The x86-64 test additionally checks the interpreter and Go native compiler.
Existing crypto known-answer tests are unchanged and pass (47.589 seconds).

All 21 new self-host cases pass, with no target skips (169.786 seconds for
the combined run). The seven interpreter/native controls also pass, and
`make lint-all` passes. Full CI remains a merge gate.

```sh
scripts/devbox go test ./internal/e2eselfhost \
  -run '^TestSelfHostCryptoForkIR' -count=1 -v
scripts/devbox make lint-all
```

Linux validation uses the ARM64 devbox, Go 1.26.8, cross-linkers/QEMU and
Wasmtime 46. These are correctness runs, not runtime benchmarks. There are
no compiler, library algorithm or size-baseline changes in this follow-up.
Pointer-element ownership and whole-compiler bootstrap reproducibility
remain open as recorded in the parent report; these tests do not close the
ownership epic.
