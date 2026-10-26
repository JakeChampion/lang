# Complete enum types in nested match results

A nested constructor pattern can lower its match expression through a generated value block. The checker infers the result type before it checks the generated result variable.

The checker previously rejected every enum annotation with no type arguments. This rule also rejected complete nongeneric enums. Their generated result variables retained the parser's `i32` default, so valid enum results failed with E003.

`vb_annotation_of` now uses `names_whole_type` with the current scope. That existing test distinguishes complete nongeneric enums from generic enums that still need type arguments. Other annotation rules remain unchanged.

The regression uses imported enum results, generic and ordinary nested patterns, heap string payloads, and wildcard paths. It checks genuine early returns and two generic result specializations. Invalid primitive results, nominal enums, assignments, mixed arms, and generic payloads retain their specific diagnostics.

This compiler correction does not change the JPEG API or its validation rules. The separate JPEG entropy slice still needs its own acceptance evidence.

## Validation

The reduced program failed before the correction with E003 on all three targets. The Go checker accepted the reduced program.

The broader suite passed 4,311 test nodes in 193.005 seconds, with no failures or skips. It covered native checker diagnostics, typed views, ownership, enum payloads, value blocks, and genuine early returns. Both compiler and standard-library complexity gates passed with unchanged thresholds.

The final focused suite passed in 43.069 seconds without failures or skips. It included the direct interpreter, three sanitized targets, ten rejection checks, and both unchanged complexity gates.

The direct interpreter control uses an explicit generic argument for the integer specialization. Its `Ok` input has no error payload from which to infer that argument.

Full current-head GitHub CI remains separate from these local results. A queued or incomplete workflow does not establish a pass.

## Clean compiler regression checks

The clean compiler reports `f429c955df5b`.
Its SHA256 is `e6b5d751d6ad94ef2227dbbe17fcb96f0a02112a583aca1c1bcd0e5bea0a1902`.
The same executable passed the primary interpreter control and both complete image scripts.
Each script passed all 150 image and shared-reader TAP cases, plus its smoke checks.
All 61 native and 37 Wasm heap censuses were balanced with zero live bytes.
Wasm provides the leak census, without the native over-release and use-after-free detectors.

A separate imported struct-result control passed on native, ARM64/QEMU10, and Wasm.
The unchanged default allocation gate reported 34,731,162 allocations and frees.
Its 10% ceiling passed, while the 1% advisory warning remained at +9.82%.
The report contains no peak RSS measurement because this runtime has no delegated cgroup.
No baseline, threshold, exemption, or benchmark workload changed.

All 1,300 tracked Fern format checks passed.
Go build, Go vet, Go format, workflow lint, and test-selector checks also passed.
