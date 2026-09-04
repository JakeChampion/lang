# 2026-09-04 — the field-receiver append grows in place where nothing can observe it

`S { ...s, xs: s.xs.append(v) }` — the shape every self-host emitter threads its
state through — routed to `lower_arr_append_value`, which slices the whole array
and grows the copy. Per append that is O(n); per built array it is O(n²).
Callgrind on a stage-2 self-compile put `__fn___fern_arr_slice` at **15.12%** of
the run (136,114,718 Ir over 29,673 calls), `irlower__LowerState__emit` its top
caller with 8,877 of them.

`own_field_inplace_append` (2026-09-02) already handled ONE case of this: the
struct-update REUSE emitter's unique arm, where an `own` base's box is proven
sole-owned at run time. This is the general case, decided by analysis instead:
native's `fieldPlaceAppendCopies` (`internal/ir/rc_analysis.go:5716`), ported as
`field_append_inplace_sites_of` and inverted to name the sites that MAY grow in
place.

## What the analysis proves

Per function, for an append whose receiver is exactly `<root>.<field>`:

| excused | because |
|---|---|
| `s.ctrl` beside `s.ops.append(v)` | a different field never names the buffer |
| `...s` in a literal that overrides `ops` | the spread copies every field but that one |
| any read outside a RETURN expression | the function exits before it can look |
| any read outside a self-rebinding assignment | later reads resolve through the replacement |

Everything else refuses: a bare read of the root, a read of the same field, a
method call on the root, a root read from a defer action or a lambda, and a root
bound to a second name anywhere (`var t = s`). The root must be the RECEIVER or
a PARAMETER — a struct LOCAL's box is this frame's, so growing a buffer it names
and handing the result out would let the frame's own reclaim free what the
result holds.

Native records that treating `...a` as a whole-container read cost 75% of the
self-host driver's compile time. The spread rule is the same fact from the other
side, and it is what makes `LowerState.emit` admissible at all.

**Counting is the safety net.** The total occurrences of the root come from
`astwalk.collect_idents_*`, which walks everything; the excused-read walk
classifies the ones it recognises. A site is admitted only when the two agree,
so an expression shape the walk does not know refuses the site rather than
admitting it. That is the difference between "the walk is complete" (unprovable
by inspection in a 74k-line file) and "an incomplete walk fails safe".

## Why the caller half is not separable

A parameter is borrowed: the caller's binding aliases the same box at the same
refcount, so a growth the callee cannot observe is fully observable to a caller
that keeps its argument live. This is #4873 exactly, reached through the struct
form — and the #4873 port's own closing sentence said why it had not needed the
struct case:

> The struct- and field-read append forms already route through the CLONE
> lowering (lower_arr_append_value), so bare-ident params are the only seeding
> shape.

Removing that assumption is the whole change, so the containment lands with it.
`grow_param_flags_of` gains native's `growFieldBufs` bit (`rc_analysis.go:6251`),
its mask position 0 is now the RECEIVER, and the dying pass-through fixpoint
carries method receivers as well as free-call arguments. Two registries —
`grow_recv_fns` and `grow_field_positions` — drive an inc-before / dec-after
bracket over the array-field buffers of a surviving struct receiver or argument.
`__fern_arr_push` copies at rc != 1, so the bracket is the whole containment.

Three details are load-bearing:

- **An owned-by-default position is not exempt.** The caller's retain is on the
  BOX; a field buffer inside it stays at its own count. Only a declared `own`
  position is skipped (the binding is dead, E051).
- **The bracket walks field CHAINS**, on both sides: an argument written
  `push(o.inner, v)` aliases the container's sub-heap, and the buffers reachable
  from a struct field are enumerated to depth 3 with a cycle guard
  (`grow_field_paths_of`, native's `arrayFieldPaths`).
- **`grow_exempt_names_of_stmt` had to learn the method receiver.** `s = s.emit(op)`
  and `return s.emit(op)` are the dying shapes; without them every threaded
  receiver is bracketed at its call and the copy lands once per link — strictly
  worse than the clone this replaces.

## Measured

Self-host compiler emitting `checker.fern` (x86-64, 4-core container, `-o /dev/null`):

| | wall | `__arr_push_shared_count` | bytes |
|---|---|---|---|
| before | 5.78 s | 376,969 | 202,926,200 |
| callee half only | 5.29 s | 376,971 | 202,922,936 |
| both halves | 5.40 s | 377,739 | 207,159,480 |

The cliff counters barely move because neither form was crossing the SHARED
cliff: the clone was a fresh rc==1 buffer at len == cap, so it took the grow
path. What the change removes is the slice and one allocation per append, plus
the geometric-growth amortisation the clone form could never have. The 770-crossing
/ 4.2 MB rise between the last two rows is the caller-side bracket doing its job.

`St.emit` in the probe drops `__fern_arr_slice` + `__fern_arr_push_owned` for one
`__fern_arr_push`.

## The traps this sets

**Both halves are needed and each is individually invisible.** Stubbing the
receiver bracket leaves the differential suite's method case exiting 2 instead
of 0; stubbing only the field bracket leaves the free-parameter case exiting 5.
Neither shows up in a wall-clock number, and neither shows up in the fixpoint,
which is self-referential.

**An answer cannot separate in-place from clone.** The clone form computes the
same result, just quadratically, so the shape test
(`TestSelfHostFieldAppendInPlaceShapeX86_64`) reads the decision off the emitted
asm. Without it a regression that quietly refuses every site is green.

## Not done

- Chains deeper than one field (`o.a.b.append(v)`) are refused outright, so the
  caller-side bracket and the callee-side admission describe the same buffer by
  construction. Native admits them.
- A struct handed to a may-grow position through anything but an ident-rooted
  place — a call result, an index — is not bracketed, and neither is an indirect
  or `dyn`-dispatched call. Native's `growBracketArgs` has the same residual;
  closing it wants the argument spilled to a scratch slot first.
- A method call on the root is treated as a whole-container read here, where
  native treats `o.m()` as the field place `(o, [m])` and excuses it when `m` is
  not the appended field. The conservative reading costs coverage, never
  soundness.
