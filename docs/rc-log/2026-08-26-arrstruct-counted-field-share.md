# The struct-literal field share sank the source's array-of-structs credit

*2026-08-26*

## What was measured

`var p: P = P { f: src, … }` where `src` is an `Inner[]` local. The construction
RETAINS `src` unconditionally — the `ExprStructLit` ident arm in `lower_expr`
sets `fav_alias_inc` on any array-of-struct / array-of-enum / leaksafe-array
field from a bare arr-slot ident, with no dependence on the source's credit — so
the field holds a counted share.

Every escape gate on the ARRSTRUCT credit read that bare ident as an escape, so
`src` was refused its credit and took the generic buffer dec: the outer array
freed, every element box and every element array field stranded.

The measurement inverts the obvious reading:

| path | native | self-host |
|---|---|---|
| share taken at runtime | 5/5 | **5/5** |
| share NOT taken at runtime | 4/4 | 4/2, 88 B |

The sharing path was already flat, because the holder's own field walk
(`__struct_arr_elems_drop_Inner`, buffer-`is_unique`-gated) did the work. It was
the path where the share did not run that leaked. "The share is uncounted" was
the wrong diagnosis: the share was counted and the SOURCE had no walk at all.

## Fix, in two parts

**1. Gate the source's element walk on the BUFFER.** `emit_arrstruct_deep_free`
walked every element and freed every element box unconditionally, gating only
each element's own field drop. A buffer this frame shares still holds elements at
rc 1, so the per-element gate cannot see the sharing. The loop is now wrapped in
`__fern_rc_is_unique(buffer)`; only the buffer dec stays unconditional. This is
the same rule `__struct_arr_elems_drop_<E>` already follows on the struct-FIELD
side.

Verified as a no-op for every shape that existed before — a sole owner is rc 1 —
across the whole probe corpus, before lifting anything.

**2. Lift the escape refusal for the counted share.** `arrstruct_counted_field_share`
answers "e is a struct literal mentioning `name` only as the bare-ident value of a
field whose declared type takes the alias-inc". Both gates take it:
`arrstruct_unsafe_for` at every statement expression position, and
`arrstruct_elem_esc_expr`'s own `ExprStructLit` arm.

Part 2 without part 1 is an over-release, and part 1 without part 2 changes
nothing. That pairing is the whole content of this slice.

## The SECOND precondition, and what caught it

The first version of this shipped to CI and **segfaulted gen2** on
`TestSelfHostStage2FixpointArm64/lexer`. Nothing on x86-64 saw it: both
matrices, `TestSelfHostRcPlanDiff`, the whole probe corpus and all three
x86-64 fixpoints were green. That is exactly the blind spot
`docs/TEST-GATES.md` names, and the reason the arm64 stage-2 leg exists — it
runs the whole compiler through its own output.

The cause: **the retain is MOVE-gated** (#6726). Where the analysis says the
construction moves the local, the box takes over its reference and BOTH the inc
and the sweep dec that cancels it are dropped (`note_moved_elided` pairs them).
This credit does not route that plain sweep dec — it routes
`emit_arrstruct_deep_free` — so granting it at a moved site frees a buffer whose
ownership left the frame.

`return T { f: src, … }` is precisely that shape, because the return is `src`'s
last use. Two sites in the compiler's own source hit it — `ModuleTables.methods`
in `build_module_tables` (a call-init local the FIRST commit had just made
creditable) and `OwnCtx.ofuncs`. Bisecting found it in four runs: commit 1
clean, the buffer gate alone clean, the exception disabled clean, the return
position alone the culprit.

The fix is not to refuse the return position — that would be a narrowing that
happens to work. `arrstruct_counted_field_share` now takes `msites` and refuses
a share whose ident is at a MOVE site, so **the credit asks exactly the question
the retain asks**. The return position stays admitted for a non-moved share.

## The precondition the guard could not carry

Granting the share took `P { ...q, … }` to **exit 99 at a perfectly flat census**
— 600 allocs, 600 frees, `live_bytes 0`, and only `__rc_underflow_count()`
dissenting. The functional-update base copies the buffer pointer into a third box
with NO inc (the design point `expr_unsafe_for`'s `ExprStructLit` arm records),
so three owners sit at rc 2 and the first two sweeps free it between them.

The share count being complete is the PRECONDITION of granting the walk, not
something the walk can guard against, so `arrstruct_share_holder_respread` is a
fourth gate on the credit beside the existing three: it collects the holders that
took a counted share of the candidate and refuses if any is used as a
struct-literal base.

## Measured after

100 rounds, x86-64:

| probe | before | after |
|---|---|---|
| `conditional` (share on one branch) | 450/350, 4400 B | **450/450** |
| `holder_escapes` | 450/350, 4400 B | **450/450** |
| `sibling_alias` (two same-named `src`) | 400/300, 4000 B | **400/400** |
| `always` (share every round) | 500/500 | 500/500 |
| `respread` | 600/600 | 600/600, and 99 without the fourth gate |
| `moved_ret` | 500/100 | 500/100, refused by the move gate |

Every exit code matches native and interp. Nothing exits 99.

The construction-retain matrix's **`struct_arr__local` flips clean** — 12 leaking
cells to 11. `struct_arr__param` stays leak for an unrelated reason:
`slot_is_reclaimable_arrstruct` refuses `i < s.n_params` outright, so a param
origin has no credit to keep.

## Still open

- `enum_arr__local` / `__param` / `__fieldread` leak the same way. `ARRENUM` has
  neither an append-built local credit nor a producer registry, so it needs both
  halves the struct side already had.
- `return P { f: src, … }` from a producer measures 500/400: the credit is
  granted and the source declines correctly, but the CONSUMER's `var p: P = hold(i)`
  is uncredited. A struct-producer question, not this one.
- `consume(P { f: src, … })` — the literal as a call argument — is still refused
  (450/150). The exception is expressed for a statement whose expression IS the
  qualifying literal; nested inside a call it falls back to the shared walker.

## Gates

**`TestSelfHostStage2FixpointArm64` is the gate that mattered here** and is now
part of the pre-push set for anything touching an exit-sweep credit — 105 s
locally, against the ~170 s the whole targeted x86-64 set costs, so there is no
excuse for skipping it.

`TestSelfHostArrStructFieldShareX86_64` (new), `TestSelfHostArrStructProducerX86_64`,
`TestSelfHostConstructionRetainMatrixX86_64` (re-pinned), `TestSelfHostContainerSinkMatrixX86_64`,
`TestSelfHostRcPlanDiff`, `TestSelfHostArrArrProducer*`, `TestSelfHostArrTup*`,
the arrstruct family, `TestSelfHostLeakMatrix` (240 s together), and the three
x86-64 fixpoints (370 s). All green.
