# Share closure contract signatures across module lowering

Part of #11534 and #8171.

Each new function type needs a row for each closure environment that can supply it. Previously, the compiler constructed the callable type for every pair of a function type and a contract. If an exact match failed, it also walked the contract to find its generic variables. A generic match constructed the callable type a second time before it tried to bind the variables.

The module now prepares one entry for each source closure contract. Each entry contains the contract, its callable type and its complete list of generic variables. View analysis reads the callable type and captures from these entries. Environment discovery shares the entries across declarations, generic instances and schema rounds.

Exact matches still use structural equality. A generic match must bind all variables, including those that occur only in a capture. The compiler checks the concrete callable type after instantiation. The entries retain source order. Later passes can change parameter types and ownership modes without changing the facts for the source module.

## Stage-2 measurements

The pinned stage 0 came from release `stage0-20261125-84426d4`. It built stage 1 on main at `bccdcc62b5aa` and on the branch. Each stage 1 built its own stage 2. Callgrind counted x86-64 instructions on fixed inputs. The environment used `FERN_IR_VERIFY=quiet`, `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`. The full compile ran each stage-2 compiler from the main source tree with `-target x86-64-linux -o checker.out compiler/checker.fern internal/stdlib`.

| Fixed input | Main instructions | Branch instructions | Change |
| --- | ---: | ---: | ---: |
| Full checker compile | 95,443,172,311 | 95,401,011,674 | -42,160,637 |
| 1 repeated callback field | 52,428,277 | 52,379,914 | -48,363 |
| 32 repeated callback fields | 54,983,555 | 54,935,297 | -48,258 |
| 128 repeated callback fields | 65,448,998 | 65,400,730 | -48,268 |
| Generic closure dispatch | 456,263,536 | 456,153,190 | -110,346 |
| 1 distinct signature | 55,029,675 | 54,982,080 | -47,595 |
| 8 distinct signatures | 67,147,590 | 67,044,512 | -103,078 |
| 24 distinct signatures | 96,411,918 | 95,843,938 | -567,980 |
| Scalar control | 17,805,577 | 17,789,628 | -15,949 |

The repeated-field program puts the same callback into each record field. The distinct-signature program gives each callback a different record result. It calls every field and checks the sum with two argument counts. The generic fixture is `generic_closure_dispatch_instances` from the conformance suite. The scalar control returns zero.

Both compilers emitted the same checker binary. The full compile used 42,160,637 fewer instructions (0.0442%). The main compiler allocated 74,653,390 objects and the branch compiler allocated 74,448,834. For 24 distinct signatures the counts were 228,363 and 227,239. Each compiler freed all allocations on both workloads. The stage-2 executable sizes were 22,009,232 and 22,010,672 bytes.

## Verification

Both stage-2 compilers reproduced themselves. The conformance reports matched across 6,276 fixture and target pairs. They contained 4,467 identical binaries and 1,809 matching refusals. All 529 JSON error diagnostics matched. The six fixed checker binaries, HTTP component and both freestanding assembly outputs matched.

All 16 focused programs passed on x86-64 Linux, ARM64 Linux and WASI. Five additional programs passed 35 ordinary, leak-check and sanitizer comparisons. They include nested generic closures and two imported modules with different records named `Item`. Direct tests on three targets checked ordered exact and generic matches, owned arguments, once-only captures, unbound capture variables and an empty suffix.

The paired compilers matched the expected outcomes and diagnostic locations for 18 view-escape cases. Three stored-view programs passed 14 paired executions, including native and WASI sanitizer runs. Both lint ratchets passed.

Both compilers copied 58,518,488 shared-array bytes in 1,022,778 copies on each checker source.

The typed-view test driver also checked 18 producer and instance classifications and 3 diagnostic locations with each compiler. All 2,372 per-function ownership mutation records matched. Both compilers passed all five strict ownership census rows and both shared-array rows. Each measured 733 unproved obligations for the native driver and 71 for the WASI checker. The native checker refuted 5,373 planted imbalances, weakened 228 and missed 229.
