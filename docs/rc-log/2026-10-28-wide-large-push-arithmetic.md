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
Final qualification will bind the committed source and a clean standard build.

The new APIs do not authorize decoder or machine integration.
That integration requires a later scoped PR after this arithmetic PR merges normally.
The full retained-machine recovery and all actual M5 and epic gates remain open.
