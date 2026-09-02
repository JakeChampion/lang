# An identity reclaim released the field it carried

Found while measuring the argument-temp slice
(`2026-09-02-call-result-arg-temp.md`), and older than it: a sanitized
self-built stage1 faults on `lexer.fern`, `parser.fern` and `checker.fern`
alike, in `cfi_proc_directive`, and main's own `irlower.fern` builds a
stage1 that faults the same way. Nothing in that slice is required to see
it.

## The shape

`x86_gas_assemble_pass` threads its assembler state and rebinds it from a
callee that took `own a: X86Asm` and updated it in place:

```
a = x86_cfi_directive(a, line);
```

The callee's update reuses the box (`own_struct_update_reuse`, #6644 slice
5), so the SAME pointer comes back, and the rebind's release then ran
`__field_reclaim_X86Asm(new, old, snap)` with `old == new`.

Every per-field guard in that helper is a comparison against `new` — a
field pointer-equal in both boxes was carried, not replaced, so it is
skipped. Except one. The nested-struct / nested-enum arm (#6605)
deliberately skips the cow compare and always releases:

> A nested-struct field CARRIED through `...base` is pointer-equal in old
> and new, so the cow guard skipped it — right as a FREE, wrong as a
> release. The base copy took an `__fern_rc_inc` for it, paid to the NEW
> box's future `__struct_drop`; the OLD box's own reference then died here
> unreleased.

That argument holds exactly when there was a copy. On an identity reclaim
there was none: no base copy, no inc, and the `cfi` box had one reference.
The dec freed it, and the recorder's next directive read a quarantined
block.

## The fix

The helper returns before the field walk when `old == new`. There is
nothing to reclaim in that case by construction — every field of `old` IS
the corresponding field of `new` — and the trailing box release was already
a no-op on it (`__fern_snapshot_dec` skips a box equal to the new value).
Three backends carry their own transcription of the helper, so the guard is
written three times: after the null/low-address check in
`emit_ir_field_reclaim_one` (x86) and `emit_arm64_field_reclaim_one`, and
folded into the same `(if …)` condition in the wasm emitter.

## Pinned

`conformance/cases/struct_nested_field_identity_rebind` — a holder with a
nested struct whose in-place `own` update hands the same box back, its inner
array read back after churn. Main's compiler answers 18 and faults under the
sanitizer; the fixed one answers 18 with no finding.
`TestSelfHostIdentityReclaimKeepsNestedFieldX86_64` runs the same source
under `FERN_SANITIZE=1`, where the stale free is a finding rather than a
value that happens to still be there.
