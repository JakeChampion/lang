# Borrowed-view escape diagnostics

The primary compiler's `-check` command now reports E063 when a returned
array view would outlive its source. This includes string `as_bytes()`
views, views held in records, generic instantiations, and views passed
through another function. String-view escapes use E065. Returning a view
of a parameter or static storage remains valid, as does returning owned
bytes.

The diagnostic comes from the typed semantic IR's existing source-anchor
analysis. It records a definite escape before production discards a refused
body, and carries the view kind separately from the storage it reads.
Unrelated lowering refusals and callers refused transitively do not acquire
an escape diagnostic. Generic instances report their original declaration
once; imported declarations retain their file, line and column.

Checking shares typed production and its anchor fixed point, without
merge-copy planning, ownership verification, inlining or fusion. The
unverified intermediate representation stays private to the diagnostic
path. Production compilation keeps its existing typed refusal; this change
adds frontend diagnostics to `-check`, without adding an AST ownership
analysis or changing the Go bootstrap checker's behavior.

## Validation

The focused Linux run passes in 65.803 seconds. It covers typed escape
evidence, diagnostic codes and positions, the actual checking CLI, and
strict semantic compilation. Positive controls cover parameters, literals,
owned copies and a local reassigned to parameter storage. Two imported
generic declarations with identical local line numbers produce separate
diagnostics at their own source files.

That run also exposed pre-existing direct execution of x86 binaries on an
ARM Linux host. The unchanged compiler reproduced the first failure. The
strict tests and shared semantic runner now use the harness's explicit
QEMU runner for both compilation and program execution, as its existing
contract requires. No target or assertion was removed.

The full unit suite and all lint gates pass. The pinned-seed bootstrap
reaches a byte-identical stage-2/stage-3 fixed point: 12,480,129 bytes,
SHA-256 `f8b7ea0eded0b223510efb0912396d4b2ae8b0f3ac675e3f542660b84745ce2b`.
The final stage-2 compiler passes eleven positive and negative checking
cases, separate imported generic source locations, and a check of its own
source. The five accepted view programs also compile and run on Darwin
and core WASM.

## Checking cost

The x86 compiler image grows from 11,121,232 to 11,124,528 bytes, a
3,296-byte increase for the diagnostic implementation. The new compiler
emits byte-identical code when given the previous compiler source, so this
comparison isolates the added implementation. The Darwin compiler remains
12,480,129 bytes. No size baseline changes.

The added typed analysis has measurable cost. In a quiet, alternating
three-sample comparison against the runtime-only compiler, checking the
compiler's own source took a median 2.475220 seconds before and 4.783633
seconds after. A 10-function pilot preceded the same generator at 1,000
functions; the larger source's medians were 12.832792 and 23.229042
milliseconds. All checks succeeded. These measurements use the completed
analysis path before the final diagnostic wording change; they do not
measure generated-program runtime.
