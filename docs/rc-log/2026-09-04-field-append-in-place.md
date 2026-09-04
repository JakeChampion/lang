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
`-emit asm -o /dev/null`, three interleaved rounds under load):

| | round 1 | round 2 | round 3 |
|---|---|---|---|
| base lowering | 10.09 s | 12.00 s | 12.57 s |
| in-place | 8.13 s | 8.41 s | 8.37 s |

Best-of-three is -19%; the base leg's drift is host contention, and the in-place
leg holds ±0.3 s under the same load, which is itself the point — the clone's
cost scales with the op list and the in-place grow does not.

Callgrind over the same pair, which is host-independent (`-g`, symbols resolved
through `nm`, since valgrind does not read the self-host `.symtab`):

| | Ir | share |
|---|---|---|
| `__fn___fern_arr_slice`, base | 6,892,945,196 | 19.60% |
| `__fn___fern_arr_slice`, in-place | 1,246,317,533 | 4.30% |
| whole run, base | 35,167,222,117 | |
| whole run, in-place | 28,988,683,165 | **-17.6%** |

The residual 4.30% is `.with` and the field appends the analysis refuses, both
of which still clone. `__fern_arr_push` falls 984 M -> 387 M Ir alongside it:
the clone form fed it a fresh cap == len buffer that ALWAYS reallocated, and the
field's own buffer usually has room.

The rc==1 append-cliff counters barely move (376,969 -> 377,739 crossings on the
native-built compiler): neither form was crossing the SHARED cliff, because the
clone was a fresh rc==1 buffer at len == cap and took the grow path. What the
change removes is the slice and one allocation per append, plus the
geometric-growth amortisation the clone form could never have. The 770-crossing
rise IS the caller-side bracket doing its job.

`St.emit` in the probe drops `__fern_arr_slice` + `__fern_arr_push_owned` for one
`__fern_arr_push`.

## BLOCKED: a self-host-EMITTED compiler segfaults on the whole compiler

**This is not shippable as it stands.** `TestSelfHostPerModuleEmitAllFixpoint{,Batch4}X86_64`
is red: gen0 (native-built) emits all 55 units fine, gen1 (linked from gen0's
own units — a self-host-EMITTED compiler) takes SIGSEGV on the first batch. The
cheap reproduction needs no fixpoint harness:

```
./bin/fern-selfhost -g -target x86-64-linux -o /tmp/s2 examples/self_host/fern.fern $PWD/internal/stdlib
/tmp/s2 -target x86-64-linux -emit asm examples/self_host/fern.fern $PWD/internal/stdlib -o /dev/null
```

~90 s to build, ~50 s to the crash. The base compiler's stage 2 completes the
same input.

The crash surfaces in `irlower__LowerState__slot_of`, reading a `locals` entry
whose `slot_name` is `0xffffffffffffffff` — a freed / never-written box, not the
poison `quarantine_rc` writes. So a buffer or a box is released while the
LowerState that was just built still holds it.

What the bisection established, each a full stage-1 + stage-2 rebuild:

| variant | outcome |
|---|---|
| callee half only, no caller-side bracket | whole-compiler emit OK (this is `fsh-8224-new-g`) |
| both halves, no retain on the in-place result | SIGSEGV, peak 5.4 GB (base: OK, peak 11.2 GB) |
| retain gated on result == source | SIGSEGV, same shape |
| TWO retains on that same arm | SIGSEGV, same RSS — so the identity arm is not the one that crashes |
| retain UNCONDITIONALLY, both arms | no SIGSEGV; OOM-killed instead (137), because every buffer then sits at rc >= 2 and every append copies |
| admission restricted to SCALAR-element fields | SIGSEGV — so it is not the shared ELEMENT pointers a grow copies |

Read together: the crashing path is the one where `__fern_arr_push` GREW
(result != source, a fresh rc 1 buffer), and only a retain that also covers that
arm suppresses it. That is the signature of a second release on a buffer with
one owner — the enclosing struct literal's field override releasing the
superseded field value on the assumption the new value is disjoint from it,
which the clone form guaranteed and this form does not. Confirming that wants
`-rc-plan` / `FERN_LEAKCHECK` on the crashing unit, which is where this stops.

Note what did NOT catch it. Every targeted gate is green: the differential suite
on all three backends, all 335 fixtures on all three self-host targets,
`make check-sources`, the lint ratchet, the feature census, the rc corpus leak
gates, and stage 1 compiling every self-host module individually. Only
`fern.fern` — the whole compiler in one unit — fails, and only when the
compiler doing the work was itself emitted by this lowering. The per-module
emit-all fixpoint is the gate that has it, which is the one place
docs/TEST-GATES.md says a fixpoint still carries signal: not "the compiler
reproduces itself" but "a self-host-built compiler runs at all".

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
