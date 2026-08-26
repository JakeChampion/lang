# The array-of-enums class had only one admitted shape

*2026-08-26*

## What was measured

`ARRENUM` (#5474) was the last array-of-X element kind to get an element walk at
all, and it arrived admitting exactly one shape: `var xs: E[] = [E.A(..), ..]`, a
non-empty literal of fresh ctors, never reassigned. Measured on the compiler at
#7548's head:

| probe | shape | self-host | needs |
|---|---|---|---|
| `dp_ea_lit` | `var xs: E[] = [E.A([..])]` | 3/3 clean | — |
| `dp_ea_litret` | producer returns a LITERAL | 3/1, 80 B | producer registry |
| `dp_ea_append` | `var xs: E[] = []; xs = xs.append(..)` | 4/2, 80 B | append-built local credit |
| `dp_ea_call` | producer returns an APPEND-BUILT local | 4/2, 80 B | both |

80 bytes a round each, unbounded, against 0 on native and interp — and the two
missing shapes are the ones ordinary code has to use, because anything that
computes its elements can write neither a literal init nor a literal return.

That is three sub-slices with three independent wins, which is why they were
measured apart before any of them was written.

## Fix

All three are transcriptions of machinery the struct side already had, not new
designs:

1. **`ARENUMF:` producer registry** — `arrenum_producer_enum` in
   `return_fresh_struct_ret_fns_of`, beside `ARRTUPF:` / `ARRSTRUCTF:`;
   `collect_fresh_arrenum_names` gains a call-init arm. The enum rides the
   registry row for the same reason the `ARRENUM:` credit carries it: an `E[]`
   slot records its element type in neither `arrarr_elem` nor `struct_type`.
2. **The append-built local credit** — `ARRSTRUCTA:` (#6535) transcribed. Same
   `ARRENUM:` credit rather than a separate one: the release path is identical
   and `emit_arrenum_reclaim_store` was already cow-guarded (`old != new`), which
   is the piece that makes a self-append rebind safe. Both blunt gates
   (`arrenum_escapes`, `body_unsafe_for`) needed an append-sanctioning variant,
   since the self-append is both the one reassignment and the one non-`len()`
   use.
3. **The producer returning an append-built local** — the composition, and the
   same one #7548 made for arrstruct.

The registry arm landed in the wrong producer function first
(`opt_fresh_ret_fns_of`, whose rows reach the collectors as `opt_fresh_ret`, not
`fresh_ret`). The probe said so immediately — no movement at all — which is
cheaper than reading for it.

## What is stricter here than on the struct side

`arrenum_self_append_elem` admits only a fresh ctor element
(`fresh_rcpayload_enum_init`), with no bare-ident laxity. That is deliberate and
is why the producer question needs no separate strict predicate the way
`arrstruct_producer_ret_local` did: **this class's element walk FREES the element
box** (`emit_enum_variant_drops` frees and zeroes the slot rather than leaving a
trailing dec — its header calls that out as the one place it differs from its
siblings). A wrong admission here is a double free, not a leak.

`with` is NOT admitted, though arrstruct's self-store predicate takes it. `with`
REPLACES, so the superseded element must be released at the store, and this
class's release is a variant dispatch that frees the box. That needs its own
emitter — its own slice.

The class's escape gate stays as tight as it was (only `xs.len()`). Only the
self-append rebind is newly permitted, and only for a candidate that earned the
append-built credit.

## Measured after

200 rounds, x86-64. Every exit code matches native AND interp; nothing exits 99.

| case | before | after |
|---|---|---|
| `producer_returns_local` | 1000/400, 22400 B | **1000/1000** |
| `producer_returns_literal` | 800/200, 22400 B | **800/800** |
| `append_built_local` | 1000/400, 22400 B | **1000/1000** |
| `literal_init` | 800/800 | 800/800 |
| `sibling_alias` | 203/101, 4080 B | 203/201, no underflow |
| `append_bare_ident_elem` | 800/400 | 800/400, refused |
| `append_then_extract` | 800/400 | 800/400, refused |
| `append_foreign_rebind` | 1068/467 | 1068/467, refused |
| `producer_local_escapes` | 800/400 | 800/400, refused |

`sibling_alias` improves without becoming clean, which is the right answer: its
producer-fed binding is now credited and its parameter-alias sibling still is
not. The point of the case is the exit code, not the balance — 99 would be
main's `b` freed under it, at a byte count that says nothing.

## Still open

The three construction-retain enum-array cells are untouched and need slice 4:

- `cr_ea_share` 450/350 — the counted struct-literal field share, the enum twin
  of #7548 commit 2. **`emit_arrenum_deep_free`'s element loop is not
  buffer-gated**, exactly as `emit_arrstruct_deep_free`'s was not, and the
  consequence is worse here because the walk frees the boxes. Gate it on
  `__fern_rc_is_unique(buffer)` FIRST and verify the gate is a no-op on the
  corpus before lifting any refusal — that ordering is what kept #7548 off exit
  99.
- `cr_ea_param` 104/102 — a param origin, refused outright by the slot gate.
- `cr_ea_fieldread` 600/300 — half the frees missing, the worst cell in the
  group, and not obviously downstream of any of the above.

## Gates

`TestSelfHostArrEnumProducerX86_64` (new), `TestSelfHostConstructionRetainMatrixX86_64`,
`TestSelfHostContainerSinkMatrixX86_64`, `TestSelfHostRcPlanDiff`,
`TestSelfHostArrStruct*`, `TestSelfHostArrArrProducer*`,
`TestSelfHostRecStructArrayEnum`, `TestSelfHostLeakMatrix` (186 s together), and
the three x86-64 fixpoints. All green.
