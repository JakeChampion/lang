# `var v: T = t` — the alias that owned nothing

#7282, and the first ownership-assignment change in the #7253 sequence rather
than a resolution one. Everything since #7272 moved a credit to the right key;
this one had no recorded fact to key on, because no binding took a retain.

## What it was

| shape | interp / native | before | after |
| --- | --- | --- | --- |
| `var t: i32[] = …; var v = t;` | `100/100` 0 | `100/100` **0** | unchanged |
| tuple, bare-ident element | `200/200` 0 | `200/0` **8000** | `200/200` **0** |
| tuple, fresh-literal element | `200/200` 0 | `200/0` **8000** | `200/200` **0** |
| scalar tuple | `100/100` 0 | `100/0` **4000** | `100/100` **0** |
| struct | `200/200` 0 | `200/0` **8000** | `200/200` **0** |
| string | `0/0` 0 | `200/0` **3200** | unchanged — its own change |

`frees=0`, not a partial release: **four** releases lost, because three things
pointed at the alias at once. The bind emitted no retain (the alias-inc was
gated on `is_arr_slot`); the source lost its credit to the escape gate; and the
alias earned none of its own.

**The array class was already correct**, and that is what made the fix
findable: it retains at the bind, and its exit sweep is driven by the `is_arr`
slot FLAG rather than by a credit an escape scan can deny. It is the reference
implementation, not a fourth case.

## Duplication, not transfer — and the conditional is why

Both slots own a counted reference and both release it; the refcount arbitrates.
The alternative — leave the source un-swept and let the alias free it — fails on
one shape:

```fern
if (c) { var v: T = t; }        // on the else path nothing was transferred
```

Under transfer the source is un-swept on the path where no transfer happened, so
a leak becomes **branch-dependent** — strictly worse than the leak it replaces.
Duplication emits the inc and the dec on the same path by construction.

## The invariant, which is the whole rule

> **Only the BOX is retained at the bind, so only the BOX may be released twice.**

The alias therefore takes the box-only release and the source keeps the deep
one. Both deep classes double-freed before that split, and both said so the same
way — exit 99 with `allocs == frees` at `live_bytes == 0`:

| class | its release | what the alias gets |
| --- | --- | --- |
| struct | field walk + box dec | `"NODEEP:"` — box only |
| rc-tuple (`"TUPRCS:"`) | type-driven deep free | the shallow `"TUP:"` |
| scalar tuple, array | the box dec IS the release | the same credit |

`"NODEEP:"` and the shallow `"TUP:"` fall out of the invariant rather than being
stipulated per type. That is the sentence to read before touching either side,
and it is recorded at the bind.

## Two threading defects, one shape, and neither was visible as itself

**A class consults more than one escape scan.** Forgiving the alias in
`body_unsafe_for` — the shared gate — took the scalar tuple to `100/100` and left
the rc tuple at exactly its unfixed `200/0`. The rc-tuple class also consults
`rctuple_payload_escapes`, which kept denying silently. It read as "that class
is not covered yet", not as "you missed a gate".

**And the forgiveness has to reach every path through the scan.** Checking the
forgiven bind only in the top-level statement loop left the recursion falling
into the un-forgiving walker, so a function-scope alias worked and a
block-scoped one did not:

```
function scope   200/200  ✅        { var v = t; }   200/100   4000
```

Two dec sites missing from the emitted asm (8 against 6) and nothing else to
see. A partial thread produces a **scope-dependent** result, which reads as a
different bug entirely.

The array control could not have caught either: it consults no escape scan, so
there is nothing to thread. A reference implementation is silent on exactly the
failure modes it bypasses.

## Ordering, which had to be chosen rather than lucked into

Retain first, verified leak-neutral on all five probes including the
conditional; then the credits. Retain-without-sweep is a leak; sweep-without-
retain is a double free. Built in one pass, the two over-releases above would
have been indistinguishable from the leaks being fixed.

## Non-vacuity

`internal/e2eselfhost/self_host_container_alias_bind_test.go`, 14 cases x 3
backends. Reverting `irlower.fern` fails **8** — the eight fixed rows, x86-64
only, because these are leaks and a leak moves no exit code. The three array
controls, the two refusals (an alias that is RETURNED, an alias that is
REASSIGNED) and the string row pass either way.

`string` is left deliberately: the fourth container takes the same rule, and its
row pins that it does not start OVER-releasing while it waits.
