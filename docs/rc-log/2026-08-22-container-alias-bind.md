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
| struct | `200/200` 0 | `200/0` **8000** | unchanged — see below |
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

## The retain lands on TUPLE slots only, and the gate is an asm diff

The retain is `tuple_elems.len() > 0`, not `slot_is_rc_container` — because the
retain and the credit have to land together, and only the tuple credit is wired
here. With `is_str` in the predicate, `var sp: string = sep;` inside
`std/array`'s `join_with_last` gained a retain nothing gives back, on every
program that reaches it.

**The verification is not a byte count.** A retain that lands on a non-pointer
allocates nothing and frees nothing, so the census reads clean either way — see
the section below. The gate is the emitted-asm diff, in both directions:

| direction | check | result |
| --- | --- | --- |
| negative | a program with no tuple alias emits BYTE-IDENTICAL asm | 6/6 fixtures identical, `rc_inc` 28/28 |
| positive | a program with one emits exactly the expected retain, in the expected function | +1 `rc_inc`, in `__fn_round` only — no stdlib function touched |

## The retain and the credit must be CO-EXTENSIVE, and the emit-hash gate is what proved it

The first version retained any tuple-box source. The credit is only ever granted
when the SOURCE is a credited tuple local — so a source that is not one got an
inc nothing gives back. Two conformance fixtures caught it:

```
tuple_elem_variant_pattern    rc_inc 28 -> 29,  releases 327 -> 327
tuple_nested_tuple_pattern    rc_inc 28 -> 29,  releases 327 -> 327
```

`w @ (A(x), y) => …` desugars to a bare-ident bind of the match scrutinee, and
when the scrutinee is a PARAMETER the retain fires while the credit cannot. **The
leak census is byte-identical either side** — an unbalanced retain allocates
nothing and frees nothing — so only the emitted-asm diff sees it, and only
because the corpus contained the shape.

Gating the retain on `slot_is_reclaimable_tuple` fixed those two and immediately
broke the rc-tuple probe the other way: an rc tuple is credited `"TUPRC:"`, not
`"TUP:"`, so the alias kept its credit while the retain stopped firing and swept
a box that was never retained — **exit 99**, `allocs == frees` at `live_bytes 0`.

Both classes, and the invariant stated once: **retain exactly when the alias will
be credited.** No credited source, no retain.

## And the narrowing itself introduced a compiler crash

Narrowing the retain from `slot_is_rc_container` to "tuple only" replaced a
BOUNDS-GUARDED predicate call with a raw `se.locals[aid_slot].tuple_elems`.
`slot_of` returns **-1** for an ident that is not a local at all — a module
function name, an enum variant, `None` — so the compiler indexed its own slot
array with -1 and aborted:

```
fern: array index out of range        (exit 134, SIGABRT)
13 fixtures + 3 std/test programs, none of them containing a tuple
```

Exit 134 is also what `__fern_rc_dec`'s underflow path and arena exhaustion
produce, so the first reading of the CI log was "the retain is still unbalanced".
It was not: the message names the cause, and the fix is the guard every sibling
predicate already had. `slot_is_tuple_box` now carries it, next to
`slot_is_rc_container`, which had `if (i < 0 || i >= s.locals.len())` all along.

**The lesson is narrow and worth having: a predicate call was doing bounds work
that inlining it silently dropped.** The original line was
`se.is_arr_slot(se.slot_of(id.name))` — `is_arr_slot` guards. Reaching past the
accessor to the column it reads is what removed the guard.

## The struct half broke six integer fixtures, and why it left

The bind-side retain fires on `slot_is_rc_container` — array, string, tuple. A
struct slot is not in that predicate, so the first version added
`|| struct_type_of_slot(slot).len() > 0`. **`struct_type` is also set for enum
names and dyn tags**, so the retain landed on values that are not rc box
pointers:

```
--- FAIL: TestFernFixturesSelfHostX86_64/int_wrap             SIGSEGV in ~50ms
--- FAIL: TestFernFixturesSelfHostX86_64/smallest_int_literal SIGSEGV
--- FAIL: TestFernFixturesSelfHostX86_64/tco_sum, factorial_print,
          int_min_arithmetic, alloc_flat_consumed_append
plus both diff-selfhost shards
```

`int_wrap` is ten lines of integer arithmetic with no alias bind anywhere. The
retain reached it through the STDLIB: on that one program it changed nine
stdlib functions — `u32__pow`, `u32__log2_floor`, `u32__reverse_bits`,
`array__fold`, `array__scan`, `__fern_i32_to_string`, `string____cmp_big` — and
added nine `__fern_rc_inc` calls. The first is `var L: bigint.BigInt = Ldig;`
aliasing a struct PARAMETER, a shape no hand-written probe here used.

**The two halves are coupled and had to leave together.** Removing the struct
retain while keeping the struct credit takes `al_struct` from a leak to **exit
99** — the alias sweeps a box that was never retained, a double free at rc 1.
Strictly worse than the leak, and exactly the failure the retain-first ordering
exists to prevent.

## What the instruments did not see — and why "leak-neutral" was the wrong gate

The retain-only half was measured **leak-neutral on five probes including the
conditional**, and certified on that basis. It is what broke six fixtures that
have existed for months, in ~50 ms, on programs with no container alias in them.
The suite here was 42 subtests green on three backends, the census clean, the
underflow counter zero.

The reason the census said nothing is that it had nothing to say. **A retain
applied to something that was never an allocation changes no alloc and no free**
— it is not a leak and not an over-release, and every byte-count instrument
reads clean while memory is corrupted. That is a third census blindness beside
the over-release-into-a-freelist one, recorded on #7253 with the correct
replacement gate.

A targeted suite is also silent on the shapes it did not think of — the
counterpart to this entry's own note that the array reference implementation is
silent on the failure modes it bypasses. And the shape gap here was not a type
but a BINDING ORIGIN: every probe aliased a LOCAL; the code that broke aliased a
PARAMETER.

The retain-only half was measured **leak-neutral on five probes including the
conditional**, and certified on that basis. It is what broke six fixtures that
have existed for months, in ~50 ms, on programs with no container alias in them.
The suite here was 42 subtests green on three backends, the census clean, the
underflow counter zero.

A targeted suite is silent on the shapes it did not think of — the counterpart
to this entry's own note that the array reference implementation is silent on
the failure modes it bypasses. The fixtures corpus and the generated
differential are what thought of them, and neither is reachable from a
hand-written probe set.

## Non-vacuity

`internal/e2eselfhost/self_host_container_alias_bind_test.go`, 14 cases x 3
backends. Reverting `irlower.fern` fails **8** — the eight fixed rows, x86-64
only, because these are leaks and a leak moves no exit code. The three array
controls, the two refusals (an alias that is RETURNED, an alias that is
REASSIGNED) and the string row pass either way.

`string` is left deliberately: the fourth container takes the same rule, and its
row pins that it does not start OVER-releasing while it waits.
