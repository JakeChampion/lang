# The enum-array counted field share, and the precondition nothing counts

*2026-08-26*

The arrenum twin of `2026-08-26-arrstruct-counted-field-share.md`. Same shape,
same two preconditions — but this class hides the second one from every
instrument the project has, which is the part worth remembering.

## What was measured

`var p: P = P { f: xs, … }` where `xs` is a credited `E[]` local. The
construction RETAINS it: the `ExprStructLit` fallback arm alias-incs any bare
arr-slot ident whose field type `is_array_type_name`, which covers `E[]` even
though the gate above it names only scalar- and struct-arrays. Verified in the
emitted asm (`__fern_rc_inc` present), not assumed — the outer gate's omission of
enum arrays makes it look like no retain happens.

So the field holds a counted share, and every escape gate on the ARRENUM credit
read that bare ident as an escape anyway: **450 allocs / 350 frees, 4000 bytes
over 100 rounds**, against native's 450/450.

## Fix

1. **`emit_arrenum_deep_free` gates its element loop on the BUFFER.** Applied and
   confirmed a no-op across the whole probe corpus BEFORE anything was lifted —
   the ordering the struct side proved necessary. `__enum_arr_elems_drop_<E>`,
   the walk a struct FIELD of the same type takes, already is_unique-gated the
   buffer; this is the local-slot side of the same rule.
2. **`arrenum_counted_field_share`** lets both gates admit the share, with the
   move refusal in from the start.
3. **`arrenum_share_holder_respread`** joins the gates on the credit.

The two gate walkers are now ONE pair for both the literal/producer and the
append-built candidate. The append exception costs the former nothing — a
candidate that is not append-built has no reassignment at all, because the
collector's `reassigned` check already refused it — so branching them only risked
their exceptions drifting apart.

## The precondition nothing counts

Both preconditions were verified load-bearing by disabling them, not assumed.
They behave completely differently:

| precondition | with the gate | without it |
|---|---|---|
| `respread` | 600/600, exit 70 | **exit 99**, still 600/600 at live_bytes 0 |
| `moved_ret` | 500/100, exit 70 | 500/**400**, exit 70 |

The second row is the trap. Removing the gate makes the census look BETTER —
400 frees against 100 — because a double free counts as a free. And
`__rc_underflow_count()` stays silent, because this class FREES element boxes
(`emit_enum_variant_drops` zeroes the slot) rather than deccing them, so no
counter is bumped.

`TestSelfHostStage2FixpointArm64` does not catch it either: the compiler's own
source contains no enum-array moved share, so gen2 stays green with the gate
removed. That is the gate that caught the identical arrstruct bug, and here it
is blind.

What catches it is a **wrong-ANSWER probe**: read the payload back after the
callee returned, with allocation churn in between so the freed memory is reused,
and check the value. Without the move gate the self-host binary **segfaults
(139)** where native and interp both exit 25.

So the instrument ladder for this class, in order of what each can see:

- census → blind (and actively misleading: the broken build shows more frees)
- `__rc_underflow_count()` → blind (the walk frees, it does not dec)
- arm64 stage-2 gen2 → blind here (the compiler does not have the shape)
- reading the value back → catches it

Anything granting an enum-array release needs the last one.

## Measured after

100 rounds unless noted. Every exit code matches native AND interp; nothing
exits 99.

| case | before | after |
|---|---|---|
| `conditional` | 450/350, 4000 B | **450/450** |
| `always` | 500/500 | 500/500 |
| `holder_escapes` | 450/350 | **450/450** |
| `respread` | 600/600 | 600/600, refused |
| `moved_ret` | 500/100 | 500/100, refused |
| `moved_uaf` (200 rounds) | 1400/600, exit 25 | 1400/600, exit 25 |

**`enum_arr__local` flips clean** in the construction-retain matrix — 11 leaking
cells to 10.

## Still open

- `enum_arr__param` (104/102) — a param origin; `slot_is_reclaimable_arrenum`
  reads a credit no param slot carries.
- `enum_arr__fieldread` (600/300) — half the frees missing, the worst cell left
  in the group, and not downstream of anything fixed here.
- `str` / `str_arr` (6 of the remaining 10) hang off the whole-program STRFLDOK
  verdict and want their own scoping pass.

## Gates

`TestSelfHostArrEnumFieldShareX86_64` (new), `TestSelfHostArrEnumProducerX86_64`,
`TestSelfHostArrStructFieldShareX86_64`, `TestSelfHostArrStructProducerX86_64`,
`TestSelfHostConstructionRetainMatrixX86_64` (re-pinned),
`TestSelfHostContainerSinkMatrixX86_64`, `TestSelfHostRcPlanDiff`, the arrstruct
/ arrtup / arrarr families and `TestSelfHostLeakMatrix` (178 s together), plus
`TestSelfHostStage2FixpointArm64` and the three x86-64 fixpoints (386 s).
All green.
