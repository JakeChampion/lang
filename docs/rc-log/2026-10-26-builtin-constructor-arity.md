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

The final selected suite passed 4,190 test nodes without failures or skips in 113.087 seconds.
The full Go checker suite passed 2,000 nodes without failures or skips in 24.614 seconds.
The positive control also passed the primary interpreter.

The clean compiler reports `dfa23fa508f9`.
Its SHA256 is `68b47eac8e56bb44944fcde126bb29d80ad2173afb7ac57eaf6c52e7b0f44282`.
The raw compiler SHA256 is `2269e46e3780e06c735ee7d55375125081962514478152426a487109951bb76e`.
The standard two-stage build and bootstrap smoke checks passed.
An earlier build stopped at a full workspace disk after its bootstrap smoke check.
A retry passed after removal of the disposable Go build cache.

The unchanged allocation gate measured 34,967,167 balanced events against the upstream baseline of 34,951,426.
The drift was 0.05 percent, within the existing tolerance.
This environment has no delegated cgroup, so the report contains no peak RSS measurement.
No baseline, threshold, diagnostic exemption, or Go checker changed.

The full native image script passed 151 TAP cases, its smokes, and its negative compiler probes.
All 61 native heap censuses balanced with zero live bytes.
The formatter checked all 1,300 tracked Fern sources.
Go build, vet, Go format, workflow lint, and test-selector checks also passed.

The full Wasm image script also passed 151 TAP cases and its smokes.
All 37 Wasm heap censuses balanced with zero live bytes.
The compiler hash remained unchanged through both image scripts.
