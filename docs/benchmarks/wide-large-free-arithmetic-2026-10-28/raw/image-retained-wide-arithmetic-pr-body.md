The current large-free runtime helper uses a 16-GiB guard and 61 size classes.
The existing proof arithmetic covers one GiB and 45 classes.
This change adds separate wide range and affine-value APIs as a prerequisite for later decoder and machine work.
All sources already present on the base main commit remain byte-identical.

The predeclared range is `0 <= low <= high <= 17179869184`.
Affine values use factor zero or one, with offsets and both endpoint values bounded by `34359738367` in either direction.
The APIs return exact integer facts or exact unknown sentinels.
Low-word and high-word projections require a uniform signed floor quotient across the interval.

The independent reference contains 4474 cases across all 77 adjacent partitions and 61 ordered capacities.
It uses Python integers, ordered capacity lookup, bitwise word reconstruction, and exact predicate checks.
The driver also executes both unchanged legacy arithmetic drivers.
Three private semantic defects fail at the exact required signed-floor, reconstruction, and wide-rounding checks.

Validation on committed source `5383d709b477ff52737c8915b93a54e4790699d2`:

- The clean standard build, version, and smoke checks passed in 89.918 seconds.
- The newly built raw compiler matches the qualified prerequisite compiler bytes.
- The stock native, ARM64, and Wasm runs passed all 4474 cases with nonzero balanced zero-live assertions.
- Three direct production-CLI runs passed with immutable source and binary hashes and nonzero balanced zero-live counts.
- A fresh Go oracle from this committed source passed check and interpreter execution.
- Format, Go build, vet, workflow lint, CI self-tests, runtime bundle, gofmt, and 219 source-audit nodes passed.
- The unchanged complexity gate passed three nodes with zero skips. Runtime checks passed all five target sets plus stock feature and fixture checks.
- The broader source check continues. CI remains unqualified.

PR #13728 corrected the ARM64 emitter defects before this work resumed.
All seven arithmetic files remained byte-identical throughout that prerequisite.
The old whole-interval word-crossing case remains, with unknown projections and six additional exact subinterval controls.
The helpers had no numerical failure.
Their predeclared bounds, reference expectations, seeds, and policies stayed unchanged.

Refs #13725 and #13717 under #13687.
No decoder or machine imports the new helpers.
The full retained-machine recovery and all actual M5 and epic acceptance gates remain open.
