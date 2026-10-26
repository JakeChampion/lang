# Source arity for builtin enum constructors

A builtin payload constructor requires one source argument, including when its generic payload type is `void`.
The documented unit value is `()`.
Thus `Ok(())` supplies a unit payload, while `Ok()` omits that argument.

The self-host checker previously returned before it consulted builtin constructor metadata.
These constructors have no user-enum declaration in that lookup path.
The checker accepted omitted and extra arguments for `Some`, `Ok`, and `Err`.
The Go oracle correctly reported E036.

The early-return path now checks the count from the builtin payload model.
Qualified names retain their enum context.
Declared enum payload checks and lexical bindings retain their previous paths.
Nullary values and void-returning calls retain their separate diagnostics.
No generic payload type receives an argument-count exemption.

The existing JPEG header helper now returns `Ok(())`.
Its earlier `Ok()` return violated the documented source contract.
The unpublished entropy helpers require the same correction after this prerequisite merges.
This correction does not justify a codec workaround or a weaker Go checker.

## Validation

The reduced matrix and native before-fix suite preserve the disagreement.
The before suite reported 29 failed test nodes and no skips.
Explicit unit payloads passed its three runtime targets.
The Go checker also rejected the invalid existing header return.

The focused controls check omitted and extra arguments, qualified constructors, wrong payload types, nullary values, and void calls.
The positive control exercises unit propagation, an optional unit value, a heap error payload, and a local function named `Ok`.
Native, ARM64/QEMU10, and Wasm executions require nonzero balanced heap counts and zero live bytes.
Both unchanged complexity gates and the broader checker diagnostics remain part of validation.
Full current-head CI remains a separate result.
