# Retire the shallow struct-copy operation and runtime

The typed lowering emits functional struct updates through explicit field
operations. Nothing in production builds `op_struct_copy`; its only caller
was an allocation-verifier fixture constructing the old operation by hand.

The obsolete constructor, native runtime routines, WASI emitter, SSA lift
and runtime-call mapping, heap classification and exported symbol are
removed together. Id 198 remains reserved. The registry driver checks that
it is retired, has no name, and that `struct_copy` no longer resolves to an
op id. The stable-id fixture removes only that registration; other ids
retain their values.

The functional-update cases retain their value assertions for array fields,
string-array fields, bare strings, multiple overrides and repeated updates.
They now assert that the retired runtime helper is absent from emitted
assembly. The allocator fast-path test bounds its inspection with the next
live helper instead of the deleted symbol.

The registry, structural/stack/FIP/provided verifiers, allocator fast path,
functional array-field updates and complexity checks pass. Source lint
passes, and bootstrap stage 2 equals stage 3. The compiler is 5,264 bytes
smaller against the release-cleanup base.

Functional updates and in-place reuse also pass their interpreter, x86-64
and WASI checks. Workflow lint and test-name checks pass after the test
rename. ARM64 reuse execution remains a native CI gate.

Thirty-two paired emissions (eight benchmarks on four targets) change
only the deleted native helper and its export declaration. WASI outputs
are byte-identical. The previous compiler's listings still contain the
helper, so they fail the new absence assertions.
