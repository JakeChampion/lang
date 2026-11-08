# Checked arithmetic keeps local Options in scalar slots

ALAC residual decode uses checked addition for each residual value.
The old compiler allocates a Some box for each successful addition.
A 10,000-iteration native probe reports 10,000 such allocations.
The immediate match only needs a presence flag and the integer payload.
Issue #14720 tracks this cost.

The Option split pass now accepts checked arithmetic as a local constructor.
It retains the existing escape, finalizer, schema, copy, and phi checks.
The cleanup pipeline applies this pass to eligible functions after other rewrites.
An Option that escapes through a return, call, or storage keeps its normal representation.

Two semantic operations represent the scalar result.
`checked_present` uses the existing portable overflow checks and produces a boolean.
`checked_value` uses that flag to select the arithmetic result or zero.
The false branch performs no division or shift.
The semantic verifier checks operator names, operand types, flag types, result types, and arities.
The physical pass emits the same target-independent IR for every backend.

The checks retain all seven operators and all five integer types.
They include zero divisors, signed minimum divided by negative one, overflow, and invalid shift counts.
Independent arbitrary-precision test calculations supply the expected results.
The native and WASI tests also check allocation counts and escaped Options.
The native 10,000-iteration probe now reports zero checked-arithmetic allocations with identical results.
