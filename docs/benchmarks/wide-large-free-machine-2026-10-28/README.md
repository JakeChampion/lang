# Separate proof for the wide large-free helper

Issue #13744 adds a separate symbolic machine after the wide arithmetic and decoder prerequisites.
The proof covers all 61 capacity classes and all 77 accepted normal partitions from zero through `2^34`.
A separate predicate represents every oversized unsigned value through `2^64-1`.
The old models, old driver assertions, and retained-driver entry points remain unchanged.

The current helper loads its guard limit into `rax` before the unsigned comparison.
The oversized return therefore retains that exact limit in `rax` and preserves all other registers and memory.
The normal return establishes the exact class, prior freelist head, table base, and two required memory writes.
The proof assumes valid, disjoint block, stack-return, and freelist regions.

A complete numeric fact remains independent of its two word projections.
The accepted interval `10737418241..12884901888` crosses word quotients two and three.
Both projections are unknown, but the full affine value remains known and selects class 58.
The proof retains this interval without refinement or a special case for its endpoints.

The new machine reuses the accepted arithmetic bounds and strict standalone decoder.
It preserves opaque register and memory identities through full-width moves and exact partial-word effects.
A bounded instruction loop follows explicit control-flow transitions and rejects revisited instructions, unknown branch outcomes, and absent returns.
The proof permits only the declared safe reads and exact full-width writes.

The independent Python reference uses whole bit-vector expressions and integer interval reasoning.
Its fixed catalog contains 336 programs, with 160 proofs and 176 rejections.
The catalog includes every normal partition witness, all register identity controls, memory and guard faults, namespace controls, and seeded label variants.
The legacy source and three former positive expectations remain explicit provenance.

The historical 32-bit left shift remains valid under the current wider contract.
The first shift count stays below 32 except at the isolated aligned upper endpoint.
The old 32-bit bit scan, right shift, and clamp comparison do not preserve the wider contract.
Their current rejections retain the predeclared counterexample evidence.

Private verifier defects permit an unsafe slot scale, omit the last partition, or expect the wrong oversized return value.
Each defect must compile and run normally before the strict collector rejects its false acceptance.
The collector requires exact ordered results, zero exit status, and nonzero balanced censuses with zero live bytes.
It preserves complete prefixes, checkpoints, and failed-worker output before rejection.

Production emitters, runtime instructions, standard library files, mathematical domains, baselines, and thresholds remain unchanged.
These symbolic regions do not establish a physical memory or process RSS bound.
Integration into the retained-driver group requires a later scope after this proof merge.
Full retained-machine recovery, actual CI recovery, and all actual M5 and epic gates remain open.

## Final qualification

The final campaigns use source `12129f769fa75a9327892e6535dbef12f7b51a27`.
The standard two-stage build passed make, version, and smoke checks in 87.472 seconds with bootstrap pin 919.
Its raw compiler matches the prior qualified production compiler byte for byte.
The published CLI records its actual source version.

All three targets completed the same 336 programs, with 160 proofs and 176 rejections per target.
All 126 fresh workers exited successfully with nonzero balanced censuses and zero live bytes.
Every proof result matches exactly across native, Wasm, and ARM64.
All campaign source and compiler hashes remained unchanged.

Final stock model and packet tests passed 29 nodes, and unchanged complexity checks passed three nodes, with zero skips.
The source gate passed all five runtime sets, fixture checks, the compiler, standard library modules, and all 83 standalone drivers.
The nonverbose source-gate command does not establish a separate test-node count.
Format covered 2,104 inputs, and build, vet, workflow lint, test names, CI self-tests, and all 219 source-audit nodes passed.

The fixed reference reproduced exactly, and all 12 protocol self-tests passed.
A fresh Go oracle from the qualified source passed check and interpreter commands.
The Go record contains its post-run binary hash.
The tool records identify their earlier baseline observations and their confirmation after the final checks.

All three private verifier defects compiled and ran normally with nonzero balanced zero-live censuses.
The strict collector rejected each false acceptance for its exact case.
The real failure-prefix control preserved eight complete records, the checkpoint, and failed-worker output.
A second attempt rejected existing output without changes.

The first generator attempt accidentally matched a branch-label suffix instead of the complete return instruction.
Its assertion stopped before reference output or Fern execution.
The corrected generator anchors the complete instruction line and preserves all declared expectations.
The archive retains the original script, failed log, and correction record.

The fresh readonly native observations include a deliberate bit-scan fault after 214 complete records.
The first uncompleted input is 4294967296, where that 32-bit bit scan reads zero.
No normal completion is claimed for that mutant.
The remaining observations, earlier source-specific right-shift capability, setup corrections, and all source identities remain distinct in the archive.

`evidence.json` binds the archive hashes and qualification facts.
The three compressed raw records preserve their exact original bytes.
Executables remain at their original paths with hashes in `omitted-executable-identities.json`.
The old models, interfaces, assertions, production instructions, domains, baselines, and thresholds remain unchanged.

The raw production compiler is unchanged, so this scope does not repeat the separate image corpus.
PR #13734 preserves that earlier current-compiler image qualification.
Retained-driver integration requires a later scope after this proof merge.
Full retained-machine recovery, actual CI recovery, and all actual M5 and epic gates remain open.
