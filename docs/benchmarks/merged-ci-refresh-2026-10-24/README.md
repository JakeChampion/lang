# Compiler driver sizes after integration

[CI run 40045777538](https://github.com/JakeChampion/lang/actions/runs/40045777538)
measures main commit `e20f0ae156d4b8742522cfcebc582ca50af243b5`.
The driver-size job builds all 11 entries and records their linked sizes.
The report retains those exact byte counts.

The combined source adds checker logic, receiver hint metadata, and SIMD support.
The checker module driver grew from 2,985,696 to 3,136,256 bytes.
That is 150,560 bytes, or 5.04 percent, above its previous baseline.
The baseline now uses all 11 measured sizes.

A local rebuild at source commit `0871d66b1f98c41d0b362a1f89512c257cf1f529`
includes the new error display methods and the IR registry probe.
All 11 driver sizes match the CI report exactly.
The second report retains these local measurements.

The later changes to the uniqueness proof and its controls leave each driver's import closure unchanged.
The closure check records that comparison.
The bootstrap pin also stays the same.

The strict comparison checks the full report against the updated baseline.
