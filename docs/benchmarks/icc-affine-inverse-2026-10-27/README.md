# Bounded ICC affine inverses, 2026-10-27

Source `6546743d24a1fe9c7d3de0e7c5c8a01699c275f3` precedes all final campaigns.
Native child #13702 declared the coefficient bounds, inverse policies, and finite criterion before implementation.
The standalone kernel supports continuous monotonic functions one and two within that subset.
It preserves selected identity bits, flat policies, closest range values, cancellation, and COW isolation.

The pre-code exact-range example has upper range `1/2 + 2^-64`.
A rounded-range clamp returns `0.99993896484375` for input `nextafter(0.5, 1)`, while the correct inverse is exactly one.
The compiled control requires that ordinary method to fail and the actual kernel to pass.
The kernel retains the offset-free range and compensated target before root selection.

The finite absolute-error criterion remains `1e-12`.
Independent 90-digit Decimal forward equations and 112 monotonic bisection steps supply the reference.
Exact Fraction flat coordinates and declared identity, endpoint, and dyadic controls supplement it.
There was no numerical failure, seed exclusion, domain reduction, numerical error waiver, or tolerance change.

Each target passes 1,581 cases: 1,496 numeric results and 85 expected errors.
All 594 fresh workers exit normally with nonzero balanced zero-live heaps.
All three sample records match exactly, with unchanged source and tool inventories.

The largest observed absolute error is `1.7056711454934297e-16`.
The records retain 1,256 native hashes and 1,257 hashes for each runner target.
The inventory includes all compiler sources and 163 standard library files.
The reference bundle reproduces exactly with SHA-256 `8bd06da9e2de126b6d206d51a6b776955c96b07f9d100f105419a468ab84443a`.

All three full runs pass 310 TAP tests with balanced zero-live censuses.
Native, Wasm, and ARM64/QEMU10 report 83, 59, and 44 censuses respectively.
The ARM64 run uses 44 standalone executables, not the whole Go ARM harness.
Each full run preserves all 1,351 selected source hashes and the compiler hash.

Both unchanged complexity gates pass with zero skips.
All 2,076 selected format files, Go build and vet, workflow checks, runtime bundle checks, and eleven protocol tests pass.
The committed-source Go check and interpreter pass all eight groups.

The clean compiler build source is `9c4085d83796cb46356a42ea51aa4ad7df69c2db`.
Its compiler and standard library match this campaign source exactly.
The existing Go oracle build source is `8cb3ccf00e945ffda49c889e6cb5b037ebc2976b`.
Its unchanged implementation loaded the new committed source.
Both source identities remain explicit in the records.

The first provisional native run passes 1,357 vectors through 170 workers.
Before the source commit, 224 explicit coefficient-neighbour vectors supplemented that corpus.
All original cases remain unchanged, including their payloads and expected observations.
The expanded provisional run passes all 1,581 vectors through 198 workers.
Both provisional records retain their actual uncommitted-source status and do not qualify the final campaigns.

The expanded provisional run reuses the first worker only after compiler, binary, core, and worker-source hash checks.
A no-overwrite assertion rejected an archive name that already contained wrapper metadata.
The original file remained intact, and the raw record used a distinct name.
The correction record retains that distinction.

Historical raw datasets remain unchanged.
The sparse worktree restores historical code fixtures and package metadata exactly from Git before the full gates.
The separate temporary filesystem holds the disposable Go build cache without deletion of existing source or evidence.

Wider affine domains, piecewise inverses, model selection, destination composition, adaptation, and encoded admission remain separate work.
No external runtime CMM accompanies this kernel.
Finite observations do not establish universal precision, compatibility, performance, physical/process bounds, hard deadlines, or production readiness.
All actual M5 and epic acceptance gates remain open.
Substantive CI remains unqualified until its actual result.
