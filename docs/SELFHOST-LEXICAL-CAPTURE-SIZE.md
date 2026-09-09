# Lexical capture contracts: binary-size attribution

Measured on 2026-09-09, comparing callback-repair parent `8b0a485d6` with
`81870b3af`. Part of #8982, #8930 and #4451.

## CI-equivalent linked artifacts

Both revisions were built with `TestSelfHostWarmStockDriver`, using the same
Linux ARM64 container, Go 1.26.8, x86-64 emitter and in-process ELF linker.
All fifteen drivers were measured and smoke-tested: 61.416 seconds for the
parent and 59.975 seconds for the current revision. These are build-run
durations, not a performance benchmark.

| Driver | Parent bytes | Current bytes | Change |
| --- | ---: | ---: | ---: |
| fern.fern | 11553004 | 11540956 | -12048 |
| asm_load_run.fern | 8309236 | 8325836 | 16600 |
| asm_modload_run.fern | 7657332 | 7940084 | 282752 |
| asm_ir_run.fern | 7426364 | 7705020 | 278656 |
| asm_run.fern | 6983276 | 7298796 | 315520 |
| wasm_ir_run.fern | 7037132 | 7377260 | 340128 |
| wasm_run.fern | 7012220 | 7352348 | 340128 |
| wasm_runio_run.fern | 6978524 | 7318652 | 340128 |
| asm_pathprobe_run.fern | 6252124 | 6592252 | 340128 |
| ssa_lift_scan_run.fern | 6061972 | 6410596 | 348624 |
| asm_ir_elig_run.fern | 5731036 | 5645052 | -85984 |
| irlower_run.fern | 6060860 | 6405084 | 344224 |
| checker_modload_run.fern | 2149796 | 2125220 | -24576 |
| ssa_emit_run.fern | 1731692 | 1723500 | -8192 |
| ssa_run.fern | 1553596 | 1545404 | -8192 |

The whole compiler is smaller than the parent. Seven partial drivers exceed
their older checked-in baselines by 5.6 to 7.1 percent. Those programs now
need checked capture contracts even when they enter lowering without the CLI's
annotation pass. Previously they reconstructed incomplete types in lowering.
They now link the shared checker, lexical resolver and typed transformations.
The whole CLI already linked much of that functionality, so a partial driver's
increase does not describe the whole compiler's change.

## Remove avoidable code before refreshing the baseline

Generated struct and tuple cleanup calls survived on literal null arguments.
Their bodies return null without effects on that path, but the optimizer had
no function contract authorizing elimination. #8987 records the repair:
generated definitions carry `NullIdentity`; IR cleanup removes only direct
zero-input calls to those definitions. It does not infer the property from
helper names, generic ownership effects or runtime-name matches.

On identical current Fern sources, matched x86-64 CLI builds before and after
this optimization measure:

| Artifact | Before bytes | After bytes | Removed bytes |
| --- | ---: | ---: | ---: |
| Whole compiler | 12057052 | 11844060 | 212992 |
| Assembly IR driver | 8081852 | 7893436 | 188416 |

These CLI-linked artifacts include unwind metadata and are not the harness
artifacts in the first table. Do not mix the two sets of totals.

The generated-body tests independently specialize cleanup at zero, including
a type with a user-defined finalizer, and require no surviving observable
operations. Live pointers, uncertified functions, indirect calls and runtime
namespace collisions retain their calls. The full native IR suite passes;
existing finalizer runtime cases pass on x86-64, ARM64 and Wasm. The self-host
scope and per-module runtime matrices and Darwin Mach-O tests also pass.

## Remaining code is attributed to semantic functionality

Symbol-bearing x86-64 assembly-driver builds give the following text changes.
The symbol sizes sum exactly to `.text`: 7245961 to 7523274 bytes.

| Module group | Text change, bytes |
| --- | ---: |
| checker | +235077 |
| astwalk | +154249 |
| lexical | +39490 |
| capturebox | +28479 |
| callsubst | +20577 |
| irlower | -140822 |
| Remaining modules and generated functions | -59737 |
| Total | +277313 |

The checker supplies actual capture and callable types, including imported
nominals and inferred call results. The lexical resolver distinguishes
declarations before closure conversion. Capture-cell and direct-call rewrites
preserve those contracts. Replaced recursive capture reconstruction is deleted.

The shared generic syntax walker has separate instantiations for lexical
resolution and call substitution. They are not interchangeable machine code:
their context values and intermediate tuples require different typed cleanup.
Equal source shape or similar symbol size is not proof that they can share an
implementation. The measured null-call overhead is removed; remaining typed
traversal is required by the source-level lexical transformations. This is not
a claim that all future optimization opportunities are exhausted.

Refresh the complete baseline to the first table, including shrinking drivers,
so subsequent changes are measured from the actual artifacts. This refresh
accounts for required semantic functionality after removing the identified
avoidable overhead. It does not waive full CI, the enum ownership failures,
or the remaining production typed-IR integration.
