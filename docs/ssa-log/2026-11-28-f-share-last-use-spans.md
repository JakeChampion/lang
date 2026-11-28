# Share statement spans with last-use analysis

Part of #11534 under #8171.

The ownership checker collects the identifier and assignment occurrences in each function. It now records the first and last source positions for each statement in the function body during the same walk. The flow tree reads those spans when it decides whether a later use can reach a value. The spread diagnostic reads them too. Each nested lambda gets a separate set of facts. The flow tree still computes spans within nested blocks.

## Compile costs

Both stage-2 compilers read fixed checker and library sources from `f26aedd61ab7`. Callgrind counted instructions on x86-64 Linux with the verifier enabled. The compiler path, output path and arguments were identical in each comparison.

| Workload | Main | Branch | Instructions saved |
| --- | ---: | ---: | ---: |
| Full checker | 97,466,934,634 | 97,433,848,060 | 33,086,574 |
| Check only | 15,083,338,035 | 15,050,277,971 | 33,060,064 |
| One owned local | 36,852,192 | 36,848,399 | 3,793 |
| 16 owned locals | 45,804,211 | 45,764,081 | 40,130 |
| 64 owned locals | 80,347,845 | 80,191,551 | 156,294 |
| Scalar control | 17,792,604 | 17,792,604 | 0 |

The full compile produced identical binaries. Main allocated and freed 75,059,093 objects. The branch allocated and freed 74,941,729, a reduction of 117,364. Both finished with zero live bytes. At 64 owned locals the branch saved 573 allocations. The stage-2 compiler grew by 4,320 bytes.

Four repository programs cover adjacent scopes, branch transfers, nested fields and local last uses. They saved 25,563, 11,323, 13,412 and 88,969 instructions respectively. Each binary matched.

## Reproduce the comparison

Build each stage-1 compiler with the pinned stage 0. Use that compiler to build `compiler/fern.fern` again for x86-64 Linux. For each stage-2 compiler, run these commands against the same source tree. Use the same paths in both runs.

```sh
export FERN_IR_VERIFY=quiet FERN_RC_CERTIFY=1 FERN_RC_CERTIFY_MEMO=0
valgrind --tool=callgrind --callgrind-out-file=/dev/null ./cc -target x86-64-linux -o out compiler/checker.fern internal/stdlib
valgrind --tool=callgrind --callgrind-out-file=/dev/null ./cc -check compiler/checker.fern internal/stdlib
```

For the numbered workloads, declare a record `Box` with one `i32[]` field. Make `take(own b: Box)` return the first element. Declare N separate boxes in main for N equal to 1, 16 and 64. Box i holds the single element i. Start at one.

Add the result of each call to `take` to a total. Subtract N × (N + 1) / 2 and return the result. The scalar control returns zero.

## Verification

Both stage-2 executables reproduced themselves at stage 3. Two full runs of `scripts/selfhost-emit-hashes` matched for all 6,318 outcomes. Those included 4,479 identical binaries and 1,839 refusals. The report SHA-256 was `69329e18da36366123c5afde15f39f315cb03675723b946830f7f1c7a796104c`. All 537 expected-error JSON diagnostics and nine fixed target outputs matched.

The ownership programs passed 207 paired target checks on x86-64 Linux, ARM64 Linux and WASI. Tests from the last-use and fresh-spread suites passed 51 binary and execution comparisons with leak and sanitizer checks.

A separate driver compared the spans, flow nodes and reachability results against the previous walk. It covered branches, loops, lambdas, assignment targets and generated coordinates on all three targets. Both compilers passed all 115 certifier driver cases. The formatter and both compiler lint ratchets passed. The feature ratchet used the prebuilt Go test executable.

## Integration with main

On `4dab199bbf38`, both compilers passed the new struct-pattern programs and the sign-branch test on three targets. The JPEG and draw checksums also matched. The ARM64 assembly satisfied all five sign-branch checks. On these fixed checker sources the check-only count fell from 15,027,921,469 to 14,994,849,163 instructions.

Both stage-1 compilers measured 57,851,964 shared-array bytes in 1,006,270 copies on fixed main source. On branch source both measured 57,919,620 bytes in 1,006,388 copies.

Both counted 739 native and 71 WASI unproved operations. Main refuted 5,383 planted imbalances, weakened 228 and missed 229. The branch refuted 5,386, weakened 229 and missed 231.

The new body walk accounts for one refutation and two missed cases. The separate span walk accounts for three refutations. One case in the block walk moved from refuted to weakened. All other per-function results matched. The branch records its measured values for the five changed rows.
