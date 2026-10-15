# Self-host driver cache: complete local import dependencies

Refs #8278 and #8920. This is a test-cache correctness prerequisite, not a
compiler/ownership optimization or a coreutils timing result.

The previous physical-line regex saw only the first import on a line. A valid
generated SSA probe placed several imports on one line and consequently left
`ssaunits.fern` out of its cache key. After that dependency changed, the cache
could return a binary built from its older contents. Failed diagnostic runs are
retained as failures; they do not validate the changed compiler.

The repair uses the source lexer to discover imports independent of line layout,
including intervening comments and decoded paths. It also follows `pub use`
dependencies. Comment/string/f-string text is not an import. Unlexable source
fails rather than publishing a partial closure. Missing local imports still fail;
external modules remain covered by the existing compiler/stdlib fingerprint.
Unrelated local files remain excluded from the key.

The legacy best-effort regex remains only in the modload fixture filler, which
deliberately handles malformed input for compiler-diagnostic tests. It no longer
defines cached driver dependencies. No compiler implementation, emitted program,
unit plan, alias/uniqueness rule, diagnostic contract or negative test is changed.

Five new closure unit regressions fail on the original scanner. An executable
regression builds a driver whose second same-line dependency returns `17`, edits
only that dependency to return `29`, and rebuilds with the same entry/compiler/
target. The original scanner incorrectly runs `17` again; the repair runs `29`.
The actual failing baseline and successful candidate have durable JSON and
explicit exit markers. Existing harness tests and the race run pass. Source lint
passes with the normal umask 022; an earlier host run under umask 077 failed the
existing apt-fixture permission assertions and is retained separately.

## Frozen-source validation, 2026-10-15

The repair passed full local validation on base
`b45a320a4b8c49fb85a62b2a7a970c2af457c565` before committing. Earlier focused
evidence at `bcc961254b1ce56ed35c00d2c4fd9e2397677769` remains separate.

- All four standard e2e shards and four compiler shards completed with explicit
  zero exit markers and package PASS events. The 4,480 e2e discoveries, including
  the original kernel test, and 3,619 compiler discoveries are accounted for
  exactly once in the discovery/shard manifests.
- All 15 selected opt-in gates actually passed, as did
  `TestSelfHostConstFuncGen2`. Default-suite skips are not substituted for these
  executions. The coverage-guided smoke uses its default 30-second budget;
  feedback testing uses five streams of 300 guided and 300 blind iterations,
  not another 30-second budget.
- All 71 remaining Go packages completed both in the e2e coordinator and in a
  standalone `-count=1 -p 1 -parallel 2` supplement. Neither package sweep used
  cached Go test results. GNU 9.12 coreutils passed in 609.210 seconds and
  643.805 seconds respectively; these are validation durations, not controlled
  performance comparisons. Packages without tests are recorded as such.
- The unchanged `TestRunWithStackLimit` ran in a Linux 6.8 software VM because
  the host lacks `CONFIG_ELF_CORE`: explicit PASS and `KERNEL_TEST_RESULT=0`.
  Its freshly compiled test binary has SHA256
  `56db92ddfa4a2ae701efa9908bd891ab5682da0706684d00e173a6537532f355`.
- Go 1.27.1 harness race tests passed again with `-count=1` (10.274 seconds).
  `make -j 2 -k lint-fast` passed, including vet, Go/Fern formatting, deadcode,
  actionlint, selected-test-name checks, CI self-tests and generated-table gates.

The default suites recorded 76 e2e and 111 compiler top-level skips. These
include opt-ins subsequently executed explicitly and unsupported configurations;
they are not all claimed as executed. Native Darwin, native ARM memcheck,
debugger/kTLS and mutation-only nightly configurations remain separately
unexecuted where unavailable. Required ARM/x86 tooling was enabled. Scoped
corrected QEMU termios handling is correctness infrastructure, not native speed
evidence; no native benchmark ran concurrently with this validation.

The final audit requires every selected terminal event, package result, explicit
exit marker, exact-once assignment, opt-in PASS, VM proof, fresh package result,
source-file inventory and SHA256 equality across all three coordinators and the
host. All 8,781 source files matched. An earlier capacity-interrupted compiler
attempt is preserved as interrupted and excluded, even though its legacy shell
trap incorrectly wrote a zero aggregate marker. Missing-gate,
duplicate-completion, incomplete-coverage and missing-fresh-ledger negative
probes are rejected; certification was not weakened.

Local immutable proof artifacts under `/workspace/tooling/`:

- `import-closure-b45-full-fresh-final-20261015T020840.tar.gz` and its audit JSON;
  archive SHA256
  `8bdbd1a3781979741ae59493fcf55f689de24432ff077a8c672c0341668e757c`.
- `import-closure-b45-frozen-source-20261015-0204.tar.gz` and its source audit;
  archive SHA256
  `5478ede6c8b576aafe89b90a730815800db0d7a8895dadd0c244937b5308e48b`.
- `import-closure-b45-final-static-0212.json`, with race/lint log references.

The source archive contains the exact pre-summary source, including this note's
earlier pending-validation paragraph. Only this validation summary changed
after the full audit; implementation and test bytes did not. The post-static
source-hash check also passed before that documentation edit.

This evidence validates the frozen base plus the repair, not a later main or
future integrated head. Current-main integration needs its own appropriate
tests and every current-head CI check before merging. The older externally
merged documentation PR's ARM-bootstrap UDP failure/cancelled workflow is not
waived by a local x86-host pass. No compiler, ownership or coreutils speedup is
claimed by this cache repair.
