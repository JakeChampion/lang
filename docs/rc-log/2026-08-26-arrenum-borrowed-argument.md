# Passing an enum array to a borrowing callee costs it its element walk

*2026-08-26* — and the `__param` cells are not what the map said they were.

## What the map got wrong

`2026-08-26-construction-retain-remaining.md` said the five `__param` cells hang
off `slot_is_reclaimable_arrstruct` refusing a slot index below `s.n_params`. The
callee's param slot is not involved at all. Every one of those cells leaks a
CONSTANT two objects over 100 rounds — so what leaks is the CALLER's `keep`,
never the 100 per-round holder boxes the callee builds.

Measuring split the five cells into two unrelated causes:

| kind | `keep` only read | `keep` passed to a borrowing callee | callee stores it in a field |
|---|---|---|---|
| `enum_arr`, `struct_arr` | clean | **leaks** | leaks |
| `str`, `str_arr`, `enum` | clean | clean | **leaks** |

This slice closes the first column for `enum_arr`. The second is a different
question — the store has to retain — and is its own slice, as is the `struct_arr`
twin of this one (a separate escape walker, sliced the way every arrstruct /
arrenum pair before it was).

**Neither matrix cell flips here.** `enum_arr__param`'s own callee stores the
param in a struct field, so it needs the other half too. What this fixes is the
adjacent leak the cell's shape was hiding.

## The bug

`arrenum_esc_expr` admits exactly one use of the local — `xs.len()` — and read
every other position as an escape, argument positions included. So

```fern
function rd(src: E[], i: i32): i32 { return (src.len() + i) % 101; }
```

— a callee that touches nothing — cost the caller's array its element walk, and
the exit sweep emitted a bare buffer dec where the counted walk was owed. 4
allocs / 2 frees against native's 4/4. Confirmed in the asm: the clean form emits
a null check, `__fern_rc_is_unique`, the element loop (tag compare, payload dec,
element-box dec) and the buffer dec; the leaking form emits one `__fern_arr_dec`.

The binding source is irrelevant — a literal leaks exactly as a producer call
does — and so is the loop. Purely the argument position.

## Why the box flag is not the answer

`borrowable_params_of` already proves "the callee never keeps this param", which
licenses a box-only release. An element walk is a DEEP free. A callee can be
box-borrowable and still hand an ELEMENT out:

```fern
function grab(src: E[], i: i32): H { return H { e: src[0], n: i }; }
```

`grab` never keeps the array, so its box flag is '1' — and the caller's walk
would free the element box the returned `H` now holds.

This is the distinction the `TUPB:` tier already draws for rc-tuples, whose
header says it in as many words: the box flag licenses box-only releases, and a
deep free "must ask this stronger question". So this adds the array-of-boxes
sibling on the same bucketed registry, `ELB:`: flag '1' iff the box flag is '1',
the param is an array, and no ELEMENT escapes the callee — proved by the class's
own rule (`arrenum_escapes`) under an EMPTY registry, registry-independent like
`tuple_payload_borrow_flags` so the interproc fixpoint cannot oscillate.

## The instrument, again

Dropping the element check and keeping the box flag puts `element_handed_out` at
self-host **exit 99 — an rc underflow — while native and interp both exit 25**,
at a flat 1400 allocs / 1400 frees, live_bytes 0.

Worse, the same edit makes three of the refused cases read 4/4 instead of 4/2. A
census-only reading therefore scores the BROKEN compiler higher than the correct
one. Only the wrong-answer probe separates them. Third slice running where this
class's census has been actively misleading rather than merely blind.

## The trap that cost the most time here

Small probes built with a CONSTANT producer argument (`mkv(7)`) leak for an
entirely unrelated reason: the local goes dead and its release moves to a precise
box-only site. That is the #7364 const-fold trap the construction matrix's own
header warns about, and it reads exactly like this bug.

It sent three hypotheses down: "a call-bound local loses its walk" (false — a
producer-bound local read only via `.len()` is clean), "`main` is special" (false
— no main special-casing exists anywhere in irlower), and "const-folding is the
cause" (false on the real shape — the matrix cell leaks identically with a
non-constant seed).

Build every probe in this area with a non-constant seed AND a genuinely live
local, and check both against the real cell before believing a small repro.

## Verification

`internal/e2eselfhost/self_host_arrenum_borrowed_arg_test.go`, 8 cases: the two
admitted shapes, a not-passed control, and five callees that must keep refusing —
a field store, a returned param, an element extraction, an element appended
elsewhere, and the element-handed-out wrong-answer probe. Every want confirmed
against BOTH oracles.

`TestSelfHostStage2FixpointArm64` green at 125 s against a 120 s baseline, which
also says the extra per-param walk in both borrow passes costs nothing
measurable — worth checking, since `borrowable_params_interproc`'s header records
that recomputing it per function OOMs the arm64 self-compile.
