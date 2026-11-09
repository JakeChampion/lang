# AVC SPS CI baseline evidence

PR #15052 adds bounded AVC sequence header metadata inspection.
CI run [42715092156](https://github.com/JakeChampion/lang/actions/runs/42715092156) measured the existing HTTP and QTRLE benchmarks.
The performance gates found baseline changes in both directions.
The baseline updates use those measured values with all tolerances unchanged.
These measurements do not establish an AVC performance improvement.

The x86 measurements come from job [129496306737](https://github.com/JakeChampion/lang/actions/runs/42715092156/job/129496306737).
The native ARM measurements come from job [129496306939](https://github.com/JakeChampion/lang/actions/runs/42715092156/job/129496306939).
`ci-measurements.json` records the changed rows and their measured values.
The native ARM QTRLE instruction baseline remains `1559097809`.
The instruction counts stayed within their existing tolerances.

`controls.json` records checksums and emitted line counts for six matched controls.
Both controls use compiler SHA-256 `48978406291c39f67a96b1a6e428e511c5cd1614c7ed0c2788f343811b8677ff`.
The compiler source is `4a04428264dd4641ca1032cf5edc84762a883dd5`.
The parent library is `4a04428264dd4641ca1032cf5edc84762a883dd5`.
The feature library is `345f706f2295c5ce0401984d36edbb9113c1adac`.
Each pair uses the same benchmark source and target.

HTTP and QTRLE emit identical code with the parent and feature libraries on all three targets.
The changed measurements therefore precede the new AVC module.
The updates retain the measured HTTP cost increases and QTRLE size reductions.
They do not establish a throughput, quality, power, or memory improvement.
Physical device measurements remain unavailable.

The CI repair also incorporates the existing callback ownership fix from PR #15060.
`repair-controls.json` records the repeated controls after that integration.
The repaired compiler SHA-256 is `6428307f7df273cb0bbebd665456a6a68876208ae1fa169808a8424ce5c02775`.
All six emitted checksums remain identical.
