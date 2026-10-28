Parent: #13717 under #13687.

Fresh main `5acfabebbef2f87844e1389615c7aecd3288baa3` still fails the second retained-machine driver.
The existing dependency fixture expects the old first instruction.
The old decoder treats the current wide load as unknown.
This prerequisite qualifies separate arithmetic helpers before decoder or machine integration.

The read-only emitter diagnostic records the actual 16-GiB guard and the old 45-bin, 57-partition model.
An independent native capability probe checked 252 cases across all 61 current size classes.
All register and freelist checks matched exactly.
The deliberate 32-bit shift mutant returned normally but failed exact checks for 36 cases.
The capability record does not qualify a new Fern model.

Predeclared scope and numeric contract:

- Add separate wide range and affine-value proof helpers.
- Preserve the existing bounded range helper, value helper, legacy drivers, decoder, and machine byte for byte.
- Preserve all production emitters, runtime sources, standard-library sources, baselines, thresholds, and helper guards.
- Use signed `i64` interval endpoints with `0 <= low <= high <= 17179869184`.
- Use affine values with factor zero or one and signed offset bounds of `34359738367` in either direction.
- Require both endpoint values to satisfy the same affine bound.
- Require exact integer results and exact unknown sentinels, without floating-point tolerances or failure exemptions.
- Keep old bounded facts as a separate legacy API with unchanged acceptance policies.

The wide capacity table contains 61 ordered quarter-octave classes from 524288 through 17179869184.
The interval table contains 77 disjoint adjacent partitions and covers every integer input in the declared range.
A class fact is known only when both endpoints select the same capacity.
A rounding fact also requires a uniform highest bit after the minimum-size clamp.
Every declared partition must produce its required known class and rounded capacity.
The input limit describes symbolic sizes only.

The wide affine API supports validity, constants, original inputs, addition, constant negation, powers, bit scans, masks, and comparisons.
Powers cover bits zero through 34.
Comparisons return exact three-state facts for all values in the interval.
Unknown facts must cause later proof rejection.
They do not authorize a runtime fallback.

Low-word and high-word projections use exact 32-bit modulo and signed floor division.
A projection is known only when the high-word quotient is uniform across the interval.
Low-word projection preserves the input affine factor.
High-word projection returns a constant unsigned word.
A join requires a valid unsigned low word and a constant unsigned high word.
The joined signed value must satisfy the declared affine bound and preserve the low-word factor.

The independent reference must include all 77 partitions, all 61 classes, old-domain examples, and signed word boundaries.
It must cover positive and negative offsets, modulo crossings, bit boundaries, invalid factors, invalid intervals, and bound violations.
Every reference expectation must precede the final source-pinned runs.
No seed filter, threshold change, or unknown-result exemption can follow a failure.
The legacy drivers must still execute their unchanged assertions.

Acceptance:

- [ ] Preserve the clean failure, emitter diagnostic, native capability results, mutant mismatch evidence, and actual source and tool identities.
- [ ] Implement only the separate wide arithmetic helpers with the declared exact contract and unchanged legacy files.
- [ ] Qualify independent exact reference cases and meaningful controlled defects without missing cases or weakened expectations.
- [ ] Pass the new arithmetic driver on native, Wasm, and ARM64 with nonzero balanced zero-live checks, plus Go check and interpreter runs.
- [ ] Run the unchanged legacy arithmetic assertions with the new qualification driver.
- [ ] Complete the clean standard build, appropriate source checks, format, Go build, vet, and unchanged complexity gates.
- [ ] Merge one scoped READY PR normally at the exact full head before decoder or machine integration.

The full retained-machine recovery remains open under #13717.
The decoder, machine, wide oversized-path contract, and current mutation expectations require a later scoped integration after this merge.
Actual CI recovery and all actual M5 and epic acceptance gates remain open.
