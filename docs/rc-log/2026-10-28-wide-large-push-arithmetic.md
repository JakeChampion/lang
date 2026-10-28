# Wide large-push arithmetic

Refs #13725. Parent: #13717 under #13687.

The current large-free helper uses a 16-GiB size guard and 61 quarter-octave classes.
The legacy proof helpers cover one GiB, 45 classes, and 57 partitions.
This change adds separate arithmetic APIs for the wider domain.
The legacy helpers, drivers, decoder, machine, emitters, runtime, standard library, baselines, and thresholds remain unchanged.

The wide range API uses signed `i64` endpoints from zero through 17179869184.
Its 77 adjacent partitions cover every integer in that domain.
Each partition has a known class and rounded capacity.
Mixed intervals return exact unknown facts.

The affine API accepts factor zero or one.
The offset and both endpoint values must remain within 34359738367 in either direction.
Low-word and high-word projections require a uniform signed floor quotient.
The existing partition across the 12-GiB word boundary therefore returns unknown projections.

The independent Python reference uses unbounded integers, bitwise word reconstruction, and ordered capacity lookup.
The new driver checks all 4474 reference cases and executes both unchanged legacy arithmetic drivers.
Three private semantic defects fail at the required signed-floor, reconstruction, and wide-rounding checks.
No domain, expected value, seed, tolerance, or unknown-result exemption changed after a failure.

The expanded driver exposed the ARM64 emitter defects that PR #13728 corrected first.
All seven arithmetic files remained byte-identical through that prerequisite.
The unchanged expanded driver then passed native, Wasm, and ARM64 execution with the stock nonzero balanced zero-live assertions.
Final qualification binds source `5383d709b477ff52737c8915b93a54e4790699d2` and a clean standard build.

The new APIs do not authorize decoder or machine integration.
That integration requires a later scoped PR after this arithmetic PR merges normally.
The full retained-machine recovery and all actual M5 and epic gates remain open.

The final stock harness passed all 4474 cases on native, ARM64, and Wasm with zero skips.
The three direct production workers also passed, with 45291, 45291, and 45834 balanced allocations and zero live bytes.
The fresh Go oracle from the committed source passed check and interpreter execution.
All source and binary hashes remained unchanged.

The clean standard build passed in 89.918 seconds.
The broader source check, 2088-source format check, Go build and vet, source audit, runtime checks, and unchanged complexity gate passed.
The source audit passed 219 nodes, and the complexity gate passed three nodes, both with zero skips.
The newly built raw compiler is byte-identical to the qualified prerequisite compiler.

A fourth private control corrupts only the last reference row expectation.
It exits at case 4473 with the exact correct helper result and the required error reason.
The committed reference stays unchanged.
[The evidence record](../benchmarks/wide-large-free-arithmetic-2026-10-28/README.md) retains all earlier failures, corrections, and producer identities.
