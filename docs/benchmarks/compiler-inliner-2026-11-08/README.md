# Compiler inliner measurements, 2026-11-08

Status: [record] fixed-input comparisons of three inliner variants.

The selected change caches static value classification during word propagation.
One packed byte per SSA value stores eligibility and the propagated read flag.
The first scan computes eligibility and memory-read seeds.
Later scans only propagate reads through operands.
The graph, types, callee summaries, and resource owners remain unchanged during each invocation.

The change also allocates known-size false masks directly.
The existing helper retains empty results for nonpositive sizes.
These changes retain the word-propagation rules and the ownership checks.

## Controls

The parent source revision is `5f7de5a05b15043a37cb95147ffc80b11b90552a`.
The selected source revision is `30e43fdafc0767419dada63381f5b9043fcd4559`.
The fixed checker and imports use revision `d4636805f696a93b35f8461d8f29cb477d682a1c`.
Both compilers use the same stage0 producer from `bootstrap/stage0.lock`.
The target is `x86-64-linux`, and both builds omit `-g`.
The [manifest](manifest.json) records the producer, compiler hashes, source hashes, and tool versions.

Allocation counters use assembly output with the proof cache disabled.
Instruction counts use native executable output with separate, primed proof caches.
All runs keep ownership certification enabled through `FERN_RC_CERTIFY=1`.
The parent normal and counter binaries match the earlier graph-only binaries byte for byte.
The manifest records this identity between revisions `99e807b132` and `5f7de5a05b`.

## Compared variants

| Variant | Cold allocation events | Warm guest instructions |
| --- | ---: | ---: |
| Graph-only parent | 52,050,468 | 29,754,123,904 |
| Word-propagation worklist | 52,177,578 | 29,433,036,436 |
| Direct mask allocation | 52,007,486 | 29,732,061,129 |
| Direct masks and cached byte flags | 52,007,486 | 29,412,491,137 |

The selected variant removes 42,982 allocation events and 341,632,767 guest instructions relative to its parent.
These reductions are 0.0826% and 1.1482%, respectively.
Its allocation count balances with the same free count and zero live bytes.
The [allocation report](allocations.json) and [instruction report](instructions.json) retain the commands and compiler hashes.

The worklist trial reduces instructions but adds 127,110 allocation events relative to the parent.
It builds reverse operand edges and a queue.
The selected variant caches classification in its existing result vector instead.
It retains the existing scan order and avoids those extra arrays.
The table compares complete variants, rather than isolated effects of each technique.

The archive retains [worklist instructions](worklist-instructions.json), [worklist allocations](worklist-allocations.json), and its [source patch](worklist.patch).
It also retains [mask-only instructions](mask-only-instructions.json), [mask-only allocations](mask-only-allocations.json), and the [mask-only patch](mask-only.patch).
Each patch applies to the recorded parent source.
The [selected patch](selected.patch) contains the complete compiler change for this comparison.

## Memory and output

The memory probe records 562,524,216 bytes of fresh heap growth for both the parent and selected compilers.
The direct-mask variant records the same value.
Fresh heap growth measures allocator expansion, rather than cumulative allocated bytes or peak RSS.
The [memory report](memory.json) and [mask-only report](mask-only-memory.json) retain the probe results.

Every variant emits identical checker assembly and identical native executables in the respective comparisons.
The assembly SHA-256 is `e961f3d9f4fdccc89dd5fe756d886d4db0375972c8371db5c156bb7b33a241f5`.
The native executable SHA-256 is `7f5f2f60c2a584c744b5f87e6e90f21cceb536bd104c7875e3681425a298bd87`.
The selected compiler built from the published tree also matches the measured compiler byte for byte.

One interrupted instruction-count run produced no complete report.
That run contributes no result to this archive.
The final [Callgrind log](callgrind.log) contains the complete count from a successful run.
The archive also retains the [worklist log](worklist-callgrind.log) and [mask-only log](mask-only-callgrind.log).

## Combined result

The [earlier graph comparison](../compiler-graphs-2026-11-07/README.md) uses the same producer and fixed input.
Before graph reuse, the checker compile performs 52,096,031 cold allocation events and 29,767,466,522 warm instructions.
The combined graph and inliner changes remove 88,545 allocation events and 354,975,385 instructions.
These reductions are 0.1700% and 1.1925%, respectively.
The generated output remains identical in both comparisons.

These results describe execution work, rather than a wall-time speedup.
The earlier graph-only timing trial varied substantially on the shared host.
The new comparison reports exact counters without a wall-time claim.

## Reproduction

Build both source revisions with the same stage0 and target.
Omit `-g` from both builds.
Use the recorded checker checkout for both subjects.
Keep the output path identical between compilers.

For allocation counts, build each compiler with `FERN_LEAKCHECK=1`.
Run both with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.
Use `-emit asm` and check each exit status before its counter result.

For instruction counts, build ordinary compiler executables.
Prime a separate proof cache for each compiler with one native compile.
Run the same command under Valgrind 3.24.0 Callgrind:

```sh
FERN_RC_CERTIFY=1 FERN_RC_CERTIFY_MEMO=CACHE valgrind --tool=callgrind --cache-sim=no --branch-sim=no --callgrind-out-file=counts.out COMPILER -target x86-64-linux -o checker FIXED_ROOT/compiler/checker.fern FIXED_ROOT/internal/stdlib
```

For heap growth, copy [the probe source](allocator-stats.fern.txt) to `build/compiler-profile/allocator-stats.fern` in each checkout.
Build each probe with the same producer and target.
Run it on the fixed checker with certification enabled and the proof cache disabled.
The probe prints allocation events and fresh heap growth after the compile.

## Validation

The full compiler source check and format check pass.
Go vet for `e2ecompiler` passes.
All existing read-word owner cases pass on x86-64, ARM64 under QEMU, and WebAssembly.
Their sanitizer variants also pass.

The opaque-return driver now checks the classification directly on all three targets.
Its graphs cover reversed block order, seeded cycles, unseeded cycles, unused values, and functions without seeds.
It also checks constant returns and opaque consumers beside a separate memory-read seed.
The test adds its private probe only to a temporary copy of the module.

The local Go overlay removes only the unavailable `/proc/self/coredump_filter` write.
It retains `ulimit -c 0` and changes no compiler behavior.
