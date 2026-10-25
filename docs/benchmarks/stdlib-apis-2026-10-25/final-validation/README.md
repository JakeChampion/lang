# Standard library and package validation

Source: 9ed05eaa460d56f7184929a82c80c4fc0708db68.
Public parent: 445d513307cbf7e77a3951e041fa49ebf534c8a7.
Main base: ba2b7a8cb69a75160b6d8a9e7fcfa289ea15789d.

The batch fixes package boundaries, scalar protocols, text ownership, typed decode errors, constant evaluation, and hyperbolic functions.

All 2,985 Go cases pass across eight packages.
The native run passes 1,679 cases.
One optional test skips because it requires RUN_CONST_FUNC_GEN2.
The final hint and generic decode run passes all 128 cases.
The IR sweep covers all 1,097 fixtures.

The Go and native suites ran at source 3846ebb92e927b6e64f5e68166741e3eee7e725c.
The final source removes four blank lines from comment-only module chunks.
The certificate records that exact difference.
A fresh official bootstrap produces the same native compiler before and after that change.
The final hint run, runtime matrices, formatter checks, and embedded checks use the final source.

Both make bootstrap and make distcheck pass with the official stage0 image.
The smoke checks compile and run a small program and coreutils/tr.
Stage 2 and stage 3 match at 14,570,816 bytes.
The direct bootstrap and the Make targets produce the same final compiler.
All 11 strict size gates pass.

The family matrix passes all 712 commands.
Its 132 runtime censuses balance, with 104,460 recorded trace events.
The WASI wrapper checks each fixture's return value, then returns a valid command status.
The original fixture and its expected return value remain the reference.

The embedded compiler passes 136 commands from outside the checkout.
Those checks cover both interpreters, three runtime targets, and Darwin compilation.
All 1,229 Fern format checks pass.
The gofmt and vet checks pass.

The regex profile compares two call patterns with the same current library and compiler.
Each pattern checks the same 1,024 inputs and returns the same checksum.
Compilation for each call uses 57,856 allocations and 12,353,519 instructions.
One compilation uses 25,120 allocations and 4,706,078 instructions.

The hyperbolic fixture has 96 f64 inputs and 96 f32 inputs.
It checks sinh, cosh, and tanh against recorded reference bits.
It covers signed zero, subnormals, adjacent thresholds, overflow, infinity, and NaN.
Finite results allow four ULP for f64 and one ULP for f32.
Zero and infinity require exact signs and bits.
The reference comes from Python math with glibc 2.41.

Earlier benchmark capsules retain their original source scope.
Their test counts are not added to this batch's totals.
The manifest records each artifact's size and SHA-256.
