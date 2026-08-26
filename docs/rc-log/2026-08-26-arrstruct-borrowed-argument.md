# The struct-array twin of the borrowed argument — and the day the fixpoint was the only instrument that worked

*2026-08-26* — eleven hand-built probes said the weaker borrow question was safe. Three fixpoint programs segfaulted.

## The bug

Identical to `2026-08-26-arrenum-borrowed-argument.md`. A struct-array local handed
to a callee that reads nothing but `src.len()` lost its whole element walk:

```fern
function rd(src: Inner[], i: i32): i32 { return (src.len() + i) % 101; }
```

4 allocs / 2 frees against native's 4/4, the payload stranded. The binding source
is not the axis (a literal leaks exactly as `mkv(seed())` does) and neither is the
loop — purely the argument position.

## Where it lives, which is not where the container gate lives

The arrstruct credit runs **two** escape gates on the same candidate, and both must
pass: `arrstruct_unsafe_for` (the container question) and
`arrstruct_elem_payload_escapes` (the element question). The first already admitted
the argument — `expr_unsafe_for`'s ExprCall arm has had a borrowable-param arm for
a long time. The second did not: its ExprCall case fell through to a generic

```fern
for arg in c.args { if (arrstruct_elem_esc_expr(arg, …)) { return true; } }
```

whose bare-ident leaf reads any mention of the local as an escape. One gate
refusing sinks the credit, so the fix belongs there and nowhere else.

`arrstruct_elem_esc_expr` had no access to the borrow registry, so `borrowable`
is now threaded through it, `arrstruct_elem_esc_stmt` and
`arrstruct_elem_payload_escapes` — the same parameter its arrenum sibling
(`arrenum_esc_expr`) has always taken. The arm it gained is the arrenum arm
verbatim, reading the same `"ELB:"` tier off the same bucketed registry. That tier
never asked about element TYPE, so it already answered for `Inner[]`; nothing new
is computed.

## The wrong turn, in full, because the reasoning was good and the answer was wrong

The arrenum twin refuses the plain box flag and asks `"ELB:"` — box flag '1' AND no
ELEMENT escapes the callee — because `emit_arrenum_deep_free` **frees** each
element box, so a callee that merely hands one out leaves the caller's walk
dangling it. Its wrong-answer probe puts the box-flag build at exit 99.

Reading `emit_arrstruct_deep_free` says that reason does not transfer. Its
per-element step is an rc-GATED field drop (`emit_struct_field_drops_gated`,
unique-only) followed by a plain `__fern_rc_dec` of the element box. **A dec cannot
over-free a box a second owner holds a counted reference to**, and the field walk
runs only at rc 1. So the plain box flag should do.

Every measurement agreed:

| handout shape | `"ELB:"` | box flag |
|---|---|---|
| `H { e: src[0], n: i }` | 1400 / 1000, live 16000 | 1400 / 1400, live 0 |
| `return src[0]` | 1200 / 800, live 16000 | 1200 / 1200, live 0 |
| `return src[0].xs` | 1200 / 800, live 16000 | 1200 / 1200, live 0 |
| `o.append(src[0]); return o` | 1600 / 1200, live 16000 | 1600 / 1600, live 0 |
| `var e = src[0]` inside the callee | 4 / 2 | 4 / 4 |

Every one reads its payload back correctly under both, with churn in between so a
freed box is reused, and matches native and interp exit-for-exit under both. Eleven
programs, no underflow, no segfault, no wrong answer, and the weaker question
strictly ahead on frees. The whole `Arr*` reclaim sweep in `internal/e2eselfhost`
(148 s) was green on the box flag too.

`TestSelfHostStage2FixpointArm64` on the box flag: **gen2 segfaults in
`sort_wider`, `float_math` and `process_assertions`**. Green on main, green with
`"ELB:"`, both at 133 s.

The counted-reference argument is sound as far as it goes; the gap is the word
COUNTED. An element handed out **uncounted** has no such reference, and the box
flag says nothing about whether one exists. Every handout that can be written by
hand happens to get its retain from some other rule. The compiler's own sources
hold one that does not, and no probe here reproduces it.

## The instrument, inverted

The arrenum slice recorded `TestSelfHostStage2FixpointArm64` as **blind** to its
bug — "because the compiler's own source lacks the shape" — and the wrong-answer
probe as the only thing that could see it. For the struct-array twin it is exactly
the other way round: the probes are blind and the fixpoint is the only instrument
that fires, because the compiler is *made of* struct arrays.

That is the reusable lesson, and it is stronger than either slice alone: **for this
class of change the probe suite and the fixpoint have disjoint blind spots, so
neither one being green is evidence.** Run both, and when they disagree the failing
one is right.

The census stayed misleading throughout, as in every slice of this class: it reads
2823 arms and PASSES on the broken compiler.

## What still leaks

- `callee_stores_field` — the construction matrix's `struct_arr__param` cell.
  Constant 2 objects over 100 rounds (104/102). Needs the STORE to retain; that is
  the cell's other half and its own slice, exactly as on the enum side.
- `callee_returns_param` — refused by the box flag itself, before the element tier
  is consulted. Correct: the caller does not sole-own the array.
- `callee_extracts_element` and the four handout shapes — refused by `"ELB:"`, and
  they must stay refused. Sound to admit in some of those exact shapes; not
  reachable without an element rule sharper than `len()`-only, which neither class
  has.

**No matrix cell flips.** `struct_arr__param`'s own callee stores the param, so it
needs the store half too. What this closes is the adjacent leak the cell's shape
was hiding — the same relationship the arrenum slice had to its cell.

## Verification

`internal/e2eselfhost/self_host_arrstruct_borrowed_arg_test.go`, 10 cases. The
three admitted shapes assert balance; the other seven assert they still LEAK, so a
future weakening back to the box flag fails there instead of in the fixpoint. Every
want confirmed against BOTH oracles — `bin/fern -interp` and the native x86-64
backend — never read off the self-host run.

`TestSelfHostStage2FixpointArm64` green at 133 s against a 134 s baseline measured
on `origin/main`, and the `Arr*` reclaim sweep green at 118 s. (The 148 s sweep
quoted above was the box-flag build — green there too, which is the point.)
