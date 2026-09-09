# Counted scalar-array updates in the self-hosted compiler

Refs #8874 under #8920. This is the scalar-array part of the repair. Pointer
elements and the retirement of AST ownership analysis remain open work.

## Contract and implementation

An `own` argument transfers a counted reference. It does not prove that the
buffer is unique: a caller can retain a snapshot, including through a shared
struct field. On base `7028395a7`, the shared-field regression in
`self_host_with_alias_ir_test.go` returns 2 on self-hosted ARM64 instead of 0:
the snapshot has been overwritten.

The self-hosted lowerer now uses one declaration-derived registry for scalar
array ownership at calls, updates and exits. Borrowed arguments acquire a
reference only when the frame does not already own its replacement. A consumed
argument slot is cleared after argument evaluation and call brackets finish.
The callee transfers a returned parameter or drops its remaining reference;
an existing moved-alias fact suppresses a second exit drop. Function-value
trampolines borrow at their public boundary and acquire the reference when
calling a consuming target.

For `xs = xs.with(index, value)` on such a parameter, lowering retains the
evaluated receiver, evaluates index and value once in source order, then drops
the destination's reference. A runtime uniqueness test chooses between reuse
and a full-width copy. One store writes the selected buffer, and its reference
moves back to `xs`. Tracked receiver scratch ownership is available to exit
cleanup. The change uses existing IR/runtime operations, declared parameter
modes and existing move facts; it adds no alias or last-use inference.

Scalar arrays projected by foreach and match bindings use the same dynamic
ownership flag as borrowed parameters. The flag is stored by local-slot
identity, so scope retirement and shadowing cannot lose it. Each projection
bind releases an owned replacement from the previous iteration, then records
a borrow. Calls, stores, returns and exit sweeps distinguish that borrow from
a subsequently owned replacement. The binding also keeps its declared element
representation, including wide and floating-point array elements.

A payload becomes an owned transfer only when the existing pending
consuming-match drop records that exact root slot and variant field as moved.
If the root is shared, the binding acquires another child reference. This
preserves the fresh-box handback's balanced lifetime without treating an
escaping payload of a borrowed parameter as an uncounted return.

Admitted elements are `i32`, `u32`, `i64`, `u64`, `u8`, `char`, `boolean`, `f32`
and `f64`. Pointer elements need a separate child-reference protocol and are
not admitted by this registry.

## Validation

The regressions compare the interpreter with strict self-host IR on x86-64
Linux, ARM64 Linux and wasm32-wasi. They cover caller/shared-field snapshots,
all admitted element types, borrowed forwarding, repeated replacement, grow,
consumption without return, alias handback, earlier argument reads, operand
order, indirect calls and capturing/noncapturing callbacks.

The lifetime cases check that a second churn batch needs no new heap storage.
The unique-input update loop requires no allocation during its first batch
either. A wrapper checks underflows after each fixture's owning frame exits,
so a double drop in that exit sweep cannot escape the assertion.

The Go x86-64 oracle also runs the applicable new cases. Three additional
semantic fixtures fail with the unchanged Go compiler:
`own-caller-operand-order` and `own-caller-higher-order` return 1, and
`own-caller-lifetime-option-projection` returns 4 (corrupted caller snapshot).
Those remain
interpreter-checked self-host regressions, rather than treating the Go result
as authoritative. No Go compiler code changes are included here.

Reproduce the scalar gate:

```sh
scripts/devbox go test ./internal/e2eselfhost \
  -run '^TestSelfHost(WithAliasIR|OwnArrayLifetimeNativeOracle)' -count=1 -v
```

Neighboring coverage includes OwnBorrowedParamArg, FieldOwnMove,
ConsumedAppendReclaim, ArrOwnedRetRelease, AppendParamElem,
StrArrayWithReclaim, BorrowedWithInPlace, ArrReturnTransfer, NestedArrPayload,
MatchPayloadWidth, OwnedPayload, MatchPayloadRC and SharedVariantPayload.
That combined suite passed in 262.432 seconds. Its existing ARM64
MatchPayloadWidth test requires a native x86 host and skips on this ARM64
host; the new strict projection tests execute ARM64 directly.
The final scalar and return-transfer gate, including wide projections and
fresh payload handback, passes in 107.276 seconds on all three self-hosted
targets. The final fifteen-driver build and smoke run passes in 78.421 seconds.
`scripts/devbox make lint-all` passes. Full CI remains a merge gate.

## Same-source linked-size comparison

Both columns use `TestSelfHostWarmStockDriver`, without a disk build cache,
and smoke-execute all fifteen linked x86-64 drivers. Control is `7028395a7`;
candidate adds this self-hosted lowering change. Environment: `fern-devbox`,
Linux ARM64, Go 1.26.8, x86-64 cross-linker and QEMU. These are linked byte
counts, not native x86-64 timing claims.

| Driver | Control bytes | Candidate bytes |
|---|---:|---:|
| fern.fern | 11499292 | 11532364 |
| asm_load_run.fern | 8263716 | 8296788 |
| asm_modload_run.fern | 7620164 | 7657332 |
| asm_ir_run.fern | 7393292 | 7426364 |
| asm_run.fern | 6946108 | 6983276 |
| wasm_ir_run.fern | 6999964 | 7033036 |
| wasm_run.fern | 6979148 | 7012220 |
| wasm_runio_run.fern | 6941356 | 6974428 |
| asm_pathprobe_run.fern | 6214956 | 6248028 |
| ssa_lift_scan_run.fern | 6028900 | 6061972 |
| asm_ir_elig_run.fern | 5702060 | 5735132 |
| irlower_run.fern | 6023692 | 6056764 |
| checker_modload_run.fern | 2145540 | 2145540 |
| ssa_emit_run.fern | 1731692 | 1731692 |
| ssa_run.fern | 1553596 | 1553596 |

The full compiler grows by 33072 bytes. Only consumers of `irlower` grow;
the three independent frontend/SSA slices are unchanged. The added behavior
is the counted-buffer call/update/exit protocol above. Every driver remains
within the existing size gate, with no baseline or tolerance changes.

## Remaining pointer-element contract

The earlier broad prototype passed string/nested snapshots but failed the
following lifetime shape on all three self-host targets. A flat buffer drop
cannot replace child ownership. Before enabling pointer arrays, copying must
secure every copied child reference, replacement must secure the new element
before dropping the old one, and the final unique buffer drop must release
its remaining children.

```fern
@noinline
function consume(own xs: string[]): i32 {
    if (xs[0] != "old!" || xs[1] != "keep!") { return 1; }
    return 0;
}
function churn(): i32 {
    var i = 0;
    while (i < 32) {
        if (consume(["old" + "!", "keep" + "!"]) != 0) { return 1; }
        i = i + 1;
    }
    return 0;
}
function main(): i32 {
    if (churn() != 0) { return 1; }
    var before: i64 = __heap_bump_bytes();
    if (churn() != 0) { return 2; }
    if (__heap_bump_bytes() != before) { return 3; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
```

The corresponding nested-array case consumes `[[1, 2], [3, 4]]` and verifies
all four scalar elements with the same churn/heap/underflow checks. These are
unfinished operation contracts for #8874, not evidence that the epic is done.
