# Compiler driver sizes after integration

[CI run 40045777538](https://github.com/JakeChampion/lang/actions/runs/40045777538)
measures main commit `e20f0ae156d4b8742522cfcebc582ca50af243b5`.
The driver-size job builds all 11 entries and records their linked sizes.
The report retains those exact byte counts.

The combined source adds checker logic, receiver hint metadata, and SIMD support.
The checker module driver grew from 2,985,696 to 3,136,256 bytes.
That is 150,560 bytes, or 5.04 percent, above its previous baseline.
The baseline now uses all 11 measured sizes.

The follow-up changes enum patterns, module capability entries, and sidebar groups.
It changes no compiler source, standard library source, or bootstrap pin.
The CI measurements therefore apply to that follow-up.

The strict comparison checks the full report against the updated baseline.
