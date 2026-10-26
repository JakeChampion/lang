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
