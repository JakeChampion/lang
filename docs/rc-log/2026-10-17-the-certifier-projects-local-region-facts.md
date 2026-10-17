# The certifier projects local region facts

Refs #12181. This reduces work inside the existing verifier; it does not
change a verdict, skip a walk, or meet the remaining 15% cold-overhead target.

## Change

An assumed walk changes the region's parameters and local definitions. Facts
for values outside that region are the parent's facts. Build roots from the
parent with local slots reset, run the existing local root fixpoint, and overlay
the local child and origin results on the parent tables. Origins are still
classified against blank outside facts before that overlay: initializing their
classification from the parent would change the scalar-phi rule.

The region's values exactly complement `outside()`. IDs, CFG traversal,
liveness, settling, token replay and diagnostic ordering remain unchanged.
Pair-word ordering stays local first, then outside values in ascending order.

## Measurement

The archived [data](data/region-projection-2026-10-17/metadata.json) identifies
both compiler binaries, source commits, frozen input hashes and raw results.
The baseline already contains the byte-copy lowering. All measurements disable
the persistent certificate memo (`FERN_RC_CERTIFY_MEMO=0`).

| Cold workload on x86-64 Linux | Baseline | Projection |
|---|---:|---:|
| Whole compiler, Cachegrind executed instructions | 127,823,867,653 | 123,757,257,913 (-3.18%) |
| Whole compiler, seven alternating native pairs | 1.000 | median ratio 0.99755 |
| Checker, instrumented allocations | 34,645,648 | 34,654,190 (+0.025%) |

Native elapsed time is effectively unchanged. Both instrumented compilers free
every allocation and end at zero live bytes. With certification disabled both
allocate exactly 25,883,590 times, isolating the allocation difference to the
certifier. All four instrumented runs emit identical assembly.

The candidate's three alternating whole-compiler enabled/disabled pairs have a
median ratio of **1.45511**: cold certification still costs about **45.5%**,
above the 15% target. This is a partial improvement, not closure of #12181.

## Correctness evidence

- The whole compiler's full 7,509-line certificate report is byte-identical;
  ordinary executable output is also byte-identical, including every native
  timing and instruction-count run.
- Restoring the original #12135 planner mutant (supplying `kept` instead of
  `built_kept`) is rejected with exit 5: `spent without a unit of v59 (borrow
  ld_at) in b20: moved into wrap`. Disabling certification accepts the same
  mutant. Neither resulting program is run.
- All certifier cases and the complexity ratchet pass. Bootstrap stages 2 and
  3 agree, and all 11 strict driver size budgets pass on the measured candidate.

A census found no repeated exact assumed-region shape across 5,367 walks, so
this change shares outside facts directly rather than caching whole walks.
