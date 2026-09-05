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
`grow_param_flags_of` gains the field half of native's `growFieldBufs`
(`rc_analysis.go:6251`), its mask position 0 is now the RECEIVER, and the dying
pass-through fixpoint carries method receivers as well as free-call arguments.
Two registries — `grow_recv_fns` and `grow_field_positions` — drive an
inc-before / dec-after bracket over the named field buffers of a surviving
struct receiver or argument. `__fern_arr_push` copies at rc != 1, so the bracket
is the whole containment.

**The mask names FIELDS, where native carries a bit.** Native's `growFieldBufs`
says only "some array field of this parameter", so `growBracketArgs` has to
bracket every array buffer `arrayFieldPaths` can reach — depth 4, cycle-guarded.
The first cut here did the same and it does not survive contact with this
compiler: `LowerState` reaches **212** array-field buffers through `FnSigs` and
its `SigReg`s, so one threading call emitted 212 rc pairs, **97,084** over a
single self-compile, ~15,000 ops into `lower_stmt_var` alone — and the emit
segfaulted. A mask position is therefore `"0"`, `"1"` (the array param's own
buffer) or `"F:xs,ys"`, seeded from the admitted sites and merged by union
across the fixpoint. `LowerState.emit` brackets one buffer, `ops`.

Naming the fields also removes the reason the plan was ever encoded as strings:
with no paths to serialise, the bracket is a flat `i32[]` of `[slot, nhops,
(field index, width) × nhops]` that nothing has to parse back.

Three more details are load-bearing:

- **An owned-by-default position is not exempt.** The caller's retain is on the
  BOX; a field buffer inside it stays at its own count. Only a declared `own`
  position is skipped (the binding is dead, E051).
- **The bracket resolves field CHAINS**, so an argument written
  `push(o.inner, v)`, which aliases the container's sub-heap, is protected
  through `o`'s slot — and the dying-argument exemption applies only to a BARE
  ident, never to a chain read off one.
- **`grow_exempt_names_of_stmt` had to learn the method receiver.** `s = s.emit(op)`
  and `return s.emit(op)` are the dying shapes; without them every threaded
  receiver is bracketed at its call and the copy lands once per link — strictly
  worse than the clone this replaces.
- **The SOLE-OCCURRENCE death had to start propagating.** `lower_stmt` unions
  grow_sole (#6048) into `grow_exempt` for every statement, so such a name
  reaches every call unbracketed — but `grow_dying_passes_stmt` recorded no edge
  for it, so `function f(p: S): S { var t = g(p); return t; }` grew p's field in
  place AND left f's own position unflagged. That gap predates this change (it
  is equally true of the #4873 array bit); the field half would have inherited
  it. The dying-pass walk now reaches every call in every statement, with the
  occurs-once shapes still restricted to a statement's outermost call, which is
  all `grow_exempt_names_of_stmt` reads.

## Measured

**Measure STAGE 2, not the compiler you just built.** `make selfhost-cli`
produces a NATIVE-built self-host compiler, and native's `emitArrayPush` already
grows this shape in place — so the binary that loop hands you never had the
clone, and an A/B of it moves nothing but the cost of the new analysis (+0.2% Ir
under callgrind). The clone is in the SELF-HOST-built compiler. The number is
therefore: same source, compiled once by the base compiler and once by the new
one, and the two results timed on the same input.

`checker.fern` through each stage-2 compiler (x86-64, 4-core container,
`-emit asm -o /dev/null`, three interleaved rounds under load). Both stage-2
binaries are the SAME source — this branch's `fern.fern` — so the only thing
that differs is the lowering that built them:

| | round 1 | round 2 | round 3 | peak RSS |
|---|---|---|---|---|
| base lowering | 7.56 s | 9.59 s | 11.00 s | 1.79 GB |
| in-place | 6.08 s | 6.17 s | 6.29 s | 1.42 GB |

Best-of-three is -19.6%; the base leg's drift is host contention, and the
in-place leg holds ±0.2 s under the same load, which is itself the point — the
clone's cost scales with the op list and the in-place grow does not.

Callgrind over the same pair, which is host-independent (`-g`, symbols resolved
through `nm`, since valgrind does not read the self-host `.symtab`):

| | Ir | share |
|---|---|---|
| `__fn___fern_arr_slice`, base | 6,951,407,812 | 25.31% |
| `__fn___fern_arr_slice`, in-place | 1,296,723,478 | 6.03% |
| whole run, base | 27,461,947,734 | |
| whole run, in-place | 21,516,652,579 | **-21.6%** |

The residual 6.03% is `.with` and the field appends the analysis refuses, both
of which still clone. `__fern_arr_push` falls 998 M -> 698 M Ir alongside it:
the clone form fed it a fresh cap == len buffer that ALWAYS reallocated, and the
field's own buffer usually has room.

**The one workload that goes the other way.** Emitting the WHOLE compiler as a
single unit in one process — `-emit asm examples/self_host/fern.fern` — is
slower and larger, not faster and smaller:

| | wall | peak RSS |
|---|---|---|
| base lowering | 142.1 s | 10.91 GB |
| in-place | 155.3 s | 12.98 GB |

An earlier note here read "peak RSS 11.2 GB -> 5.4 GB" on this workload. That
was not a like-for-like pair: the 5.4 GB leg SEGFAULTED about 50 s in and never
finished, so it is the peak of a partial run. The 155.3 s / 12.98 GB leg was
measured with the identity-arm retain in place, and that retain is the whole of
the regression — see the section below. It is gone; the numbers in this table
describe a lowering that no longer exists.

## Two containment holes a self-host-EMITTED compiler found

`TestSelfHostPerModuleEmitAllFixpointX86_64` is the gate that had both: gen0
(native-built) emits every unit fine; gen1, linked from gen0's own units and so
a self-host-EMITTED compiler, did not. Neither shape is visible to any rc
detector — every free involved happens at rc 1, so the `rc == 0` underflow trap
never fires. A whole-compiler run under `FERN_SANITIZE`, with the quarantine
implication lifted out of `rc_free_debug_on` so the memory behaviour stays
normal and only the over-release report is armed, reaches the same crash with no
`fern-sanitizer:` line. These are uncounted-alias frees, not underflows.

### 1. The bracket released a PLACE, not the value it retained

`emit_grow_field_bracket` emitted both sides from one plan: load the slot, walk
the field hops, call the helper. The release side is therefore a SECOND
evaluation of a place, and a place is not stable across the call it brackets.

The window is real and reachable. `var s2 = lower_block(iff.then_body, s1)` with
an EMPTY then-body hands `s1`'s own box straight back at rc 1;
`release_last_use_source`'s pass-through arm then decs it, freeing a box `s2`
still names (that is #8240, pre-existing and identical on main — harmless there
because the next `emit` allocates its result box, gets the block straight back,
and writes field values it had already read onto the operand stack). With the
bracket in play it stops being harmless: `emit`'s `arr_push` grows at rc 2 into a
fresh buffer, `emit`'s struct literal is handed s2's own freed block, and the
post-call re-read of `s2.ops` therefore yields the buffer the callee just grew.
The dec lands on it at rc 1 and frees it. `__fern_arr_dec`'s free path writes the
freelist link over the block's **cap** word, so the buffer the state still holds
now reads cap 0 with len 114 — and the next append takes `.Larr_push_grow`,
picks the `$4` floor, and copies 114 elements into a four-element buffer, through
whatever came next in the arena.

The fix is `emit_grow_field_bracket_inc` / `_dec`: the retain side captures each
resolved buffer pointer in a scratch local and the release side loads it. Same
discipline `emit_arr_share` already has for a bare array argument, where the
value lives in a slot rather than being re-read.

### 2. The dying exemption covered a local that names someone else's container

With the bracket fixed, gen1 reached batch `[40:48]` and aborted 134 — an array
bounds `exit(134)`, in `sig_reg_argref`, on a registry whose co-indexed arrays
were one apart:

```
rows len=1341    next len=1342
```

`sig_reg_append` appends to two fields of its parameter, which the analysis
admits on the callee's own terms. Its caller is

```fern
var struct_ret_fns_aug: SigReg = sg.struct_ret_fns;
struct_ret_fns_aug = sig_reg_append(struct_ret_fns_aug, …);
```

The rebind is the dying self-reassign shape, so `grow_exempt_names_of_stmt`
exempted it and no bracket was emitted — but the local was bound from a FIELD of
the module-wide `FnSigs`, which stays live for the whole module emit. The
exemption's premise is that `x = f(x)` kills x's binding; it does not establish
that x is the only route to the buffers inside it. `next` had spare capacity and
grew in place while `rows` was at `len == cap` and reallocated, so the registry
kept the old `rows` and the grown `next`.

The chain is `sg.struct_ret_fns.rows` — two levels, which the one-level
`F:<field>` mask can neither express nor propagate. "Chains deeper than one field
are refused outright" was being enforced on the callee side and not on the
caller's. `grow_alias_names_of` closes it: the locals that name a container this
frame does not own — bound anywhere in the body from a field / index / slice
read, from a `for` element, or from a name already in the set — lose the
exemption, so their calls take the bracket and the callee's grow copies. The hot
threading names are bound from CALL results and from parameters, neither of
which is an alias, so the win is untouched.

## The traps this sets

**Both halves are needed and each is individually invisible.** Stubbing the
receiver bracket leaves the differential suite's method case exiting 2 instead
of 0; stubbing only the field bracket leaves the free-parameter case exiting 5.
Neither shows up in a wall-clock number, and neither shows up in the fixpoint,
which is self-referential.

**An answer cannot separate in-place from clone.** The clone form computes the
same result, just quadratically, so the shape test
(`TestSelfHostFieldAppendInPlaceShapeX86_64`) reads the decision off the emitted
asm. Without it a regression that quietly refuses every site is green. The same
holds for the bracket's pairing: `TestSelfHostGrowFieldBracketReleasesRetainedX86_64`
reads it off the asm, because a re-read only diverges from the capture once the
freelist happens to line up.

**Only a self-host-EMITTED compiler on the WHOLE compiler had either hole.**
Both were green on the differential suite, all three self-host targets'
fixtures, `make check-sources`, the lint ratchet, the feature census, and every
rc corpus leak gate. The per-module emit-all fixpoint is what has them — not as
"the compiler reproduces itself" but as "a self-host-built compiler runs at
all". Reach for it on anything that changes what a callee may do to a caller's
buffer.

## Not done

- Chains deeper than one field (`o.a.b.append(v)`) are refused outright, so the
  callee-side admission names a FIELD of the position's declared type and the
  caller-side bracket resolves exactly that field. Native admits them, at the
  cost of the bit-and-enumerate bracket this entry's first cut measured.
- A struct handed to a may-grow position through anything but an ident-rooted
  place — a call result, an index — is not bracketed, and neither is an indirect
  or `dyn`-dispatched call. Native's `growBracketArgs` has the same residual;
  closing it wants the argument spilled to a scratch slot first.
- A method call on the root is treated as a whole-container read here, where
  native treats `o.m()` as the field place `(o, [m])` and excuses it when `m` is
  not the appended field. The conservative reading costs coverage, never
  soundness.
- `grow_alias_names_of` does not cover a MATCH-ARM payload binding, which is a
  view into a scrutinee that outlives the arm exactly as a `for` element is. It
  would take reading the binding names out of `ast.Pattern`; the shape needed to
  bite is `x = f(x)` on an arm binding whose struct fields a callee grows, which
  nothing in this tree does.

## The identity-arm retain was the leak, and it is retired

The first cut had `lower_field_append_inplace` retain its result when the push
did not reallocate, on the reading that the literal the value feeds becomes a
second owner of the source container's buffer and owes the counted retain a
container field owes.

Nothing releases that retain. `__field_reclaim_<T>` frees a replaced array field
only when it differs from BOTH the replacement's and the caller's snapshot — and
the identity arm is exactly the case where it does not differ, so the superseded
box hands the buffer over and decs nothing. The retain and the cow guard are two
compensations for one hazard, and together they overcount by one per in-place
grow:

- The abandoned buffer is a straight leak, linear in the number of in-place
  grows. `TestSelfHostAppendParamElemX86_64` measured `(n-1)/2` unfreed blocks
  over an n-iteration threading loop, and `TestSelfHostFieldReclaimIRX86_64`
  exhausted the 16 GiB arena (exit 125) on the 200M-iteration builder churn.
- Leaving the field at rc >= 2 also sends the NEXT append down
  `__fern_arr_push`'s un-share copy, which allocates a fresh buffer and abandons
  the old one — the one-process whole-compiler emit's +2 GB above.

No detector outside `FERN_LEAKCHECK` sees either: every free involved is at rc 1
and every answer is correct, which is how it reached main.

`TestSelfHostFieldAppendInPlaceReclaimsX86_64` is the gate that was missing: the
feature's own cases are differential, and an answer cannot see a leak.

## The identity arm is a MOVE: the field is nulled

Retiring the retain on the reading "source and replacement never both name the
buffer past the store" broke two tests on main's own run
(`TestSelfHostRecvMoveNoDeepIRX86_64/receiver-move-inplace-append-survives-sweep`
exit 91; the `own_self_reassign_move` fixture hanging on both natives). The
reading was wrong: after an in-place grow the buffer has TWO uncounted names at
rc 1 — the root's field and the value — and the cow-guarded rebind is only one of
the paths that can release either. Four others were found, each a use-after-free
or a double free with nothing at rc 0 to trip a detector:

| route | shape | who frees the buffer the other still holds |
|---|---|---|
| return sweep of a bare-credit local | `var ms = S {…}; …; return ms.emit(4)` | the caller's `__struct_drop_S(ms)` — `emit` is in the receiver-borrow registry's `nomove` list, so `ms` is not NODEEP and the return-position death deep-drops it |
| an `own` param's exit release | `function push(own b: S, v) { var ys = b.ops.append(v); … }` | the callee's OWNREL row: `__struct_drop_S(b)` at exit, on its own root |
| the same, one call down | `function g(own s: S, v): S { return h(s, v); }` with `h` growing `s.ops` | `g`'s OWNREL exit release, after `h` handed the buffer to its result |
| the VALUE's holder | `var ys = s.ops.append(v); var n = ys.len(); return S { ops: [], n }` | `ys` is swept at exit; the caller's `__field_reclaim_S` then frees `s.ops` again |

The fix is at the site, not at the releases: on the identity arm
`lower_field_append_inplace` STORES NULL into the source field. The grow was a
move out of the box — a field's counterpart of the zeroed slot a moved local
gets — and every rc helper (`__field_reclaim_<T>`'s array arm,
`__struct_drop_<T>`, `__fern_arr_dec`, `__struct_arr_elems_drop_<T>`) already
skips a null field, so each release above finds nothing to free. The
reallocating arm is untouched: the pre-grow buffer stays the source's, differs
from the value, and the source's release frees it. `field_append_inplace_at`
also asks that the root's box type resolves, since the store needs the field
slot.

Two consequences for the admission. A body-scope host (`var`, an expression
statement, a condition) INSIDE A LOOP is refused: the next iteration would read
the moved-out field. A return exits and a rebind of the root replaces it, so
those keep their sites in loops. And the E051 caller side had a hole the `own`
probes exposed: `a = f(a, i)` into an `own` position ran the struct rebind's
`__field_reclaim_<T>` on the box the callee had already released. The fixture
only passed because `push_box` frees `b`'s box and then allocates a same-sized
result box, so the freelist handed the same block back and the `old != new`
cow guard skipped everything; a callee that allocates its result before
releasing (`g` above) reads a freed block. The struct rebind now bare-stores
when `call_owns_arg` says the callee took the box (`lower_stmt_assign`).

The four rows and the loop refusal are in `selfHostFieldAppendCases`, so all
three backends run them against the interpreter. The loop case hands the callee
a fresh call result at an `own` position: a LOCAL receiver is bracketed by its
caller (rc 2, copy), which is what made the first cut of that case pass with
the refusal stubbed out.

**#8259's `grow_return_local_filter` is the first row's bracket-side fix**:
withdrawing the return-position death forces the callee's copy, so the identity
arm is never reached. With the move it is no longer needed for this hazard —
the deep drop finds a null field — and it costs a whole-buffer copy per
return-position death of a local (measured there at +5 s / +0.33 GB on the
one-process whole-compiler emit). The two compose: with both in, the bracketed
sites copy and the rest move.
