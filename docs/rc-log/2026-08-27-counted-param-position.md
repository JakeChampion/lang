# A counted param position is not a move

*2026-08-27* — two of the five construction-retain `__param` cells, and the
correction of what they were thought to be.

## The leak

`str__param` and `str_arr__param`, and the other three alongside them, leak a
CONSTANT 2 objects over 100 rounds — never the 100 per-round holders. It is the
caller's own `keep` that is never released.

| cell | before | native |
|---|---|---|
| `str__param` | 102 / 100, 56 B | 101/101 |
| `str_arr__param` | 106 / 102, 112 B | 104/104 |
| `enum__param` | 102 / 100, 80 B | 102/102 |
| `enum_arr__param` | 104 / 102, 80 B | 104/104 |
| `struct_arr__param` | 104 / 102, 88 B | 104/104 |

The control is what names the trigger: a callee that only reads `src.len()` is
clean at 2/2. The struct-literal STORE in the callee is what costs the caller its
release.

## The asm said it without inference

In `round(src: string, i: i32)` the callee emits `__fern_rc_inc` on `src` before
boxing the holder, and `__struct_drop_P` plus the box dec at exit. `main` emits
`mkv`, `round`, `__rc_underflow_count` — and no release of `keep` on any path.

Both sides are internally consistent. The callee retains and gives the retain
back, netting zero on the incoming reference; the caller, seeing a non-borrowable
argument position, withheld the release its own creation owes. Neither owns the
original.

## The verdict already existed and was unreachable

`param_counted_of` (#6522, native's `inferParamCountedRetain`) answers exactly the
question this turns on: is every appearance of the callee's parameter a COUNTED
store or a non-retaining read? `round` earns `SCNT:round|10` already.

Its verdict was reachable only from the argument-temp stash, which holds the
sigs. The ESCAPE walker threads the borrowability registry and nothing else, so
`expr_unsafe_for`'s call-argument arm — and `strarr_expr_unsafe`'s — saw a plain
escape.

The fix folds the rows into that registry under a `"CNT:"` key prefix, at
`fn_sigs_for_borrow`, which is the one place both are computed. It is the only
tier on that registry that ADMITS where the box flag refuses; `"TUPB:"` and
`"ELB:"` both narrow it. The two ask different questions: the box flag asks
whether the callee keeps a reference at all, `"CNT:"` whether a reference it does
keep was RETAINED.

For `string[]` the caller's release is a deep element walk, and it survives
because the callee's store retains the BUFFER: `__fern_str_arr_free`'s rc gate
leaves the walk to whichever owner reaches rc 1. The tier's own rule is what
makes that safe rather than lucky — it refuses `ExprIndex` for arrays precisely
because an element read may hand a reference out uncounted.

## Scope: two cells, not five

`param_counted_of` admits `string`, scalar-element arrays and `string[]`, BY
TYPE. The enum, enum-array and struct-array `__param` cells are not counted by
any tier, so the consumer this slice adds cannot see them. Widening the tier to a
class whose release walks element boxes is the deep-release question `"ELB:"`
exists for and owes its own proof. Those three stay pinned as leaks.

## The load-bearing check, measured rather than asserted

Replacing the `"CNT:"` lookup with a blanket admission of every bare-ident call
argument breaks NOTHING here: not the eight probes, not the rc suites. It only
closes `enum__param` as well. The disabling experiments on `param_counted_of`'s
own guards — `str_result_cannot_alias`, and the refusal of `return src` — are the
same: with both off, a callee that hands the param straight back still exits 45
with its bytes intact after 200 churn allocations.

So these probes do NOT separate the tier from the blanket, and this record does
not claim they do. The lookup is kept because the blanket asserts something no
analysis established, and because refusing is the leak-safe floor for this
family. A later slice that wants the blanket owes its own proof; this one's
silence is not it.

Worth noting what the blanket experiment DID establish: `enum__param` is refused
by the same consumer, not by a different walker. When the enum tier is written,
the consumer is already in place.

## It flips a deliberate exclusion, and that is the part worth reading

`strarr-local-stored-by-callee-excluded` — pinned identically in the x86-64,
arm64 and wasm element-reclaim suites — asserted that `keep(xs: string[]): Box`
storing the param into a returned struct must LOSE the caller's element walk.
The local wasm sweep is what caught it; the targeted rc set does not run those
suites, which is a gap in the set rather than in the sweep.

The exclusion's premise was "the parameter is not borrowable". That is still
true and is no longer the whole question. What makes flipping it sound is not
that the census came out clean — it does, but this class's census lies — it is
that two independent rules close the hazard from both ends:

- `__fern_str_arr_free` is **rc-gated**, so only the owner that finds rc 1 walks
  the elements. Whichever of the caller's sweep and the holder's drop runs
  second is the one that frees.
- No element can be out **uncounted**. The tier refuses `ExprIndex` for array
  params, so the callee can neither extract an element nor pass the array onward
  to a callee that does; and the caller's own element-hazard rules still exclude
  `var t = xs[0]` — the alias case in the same suite, still pinned as an
  exclusion and still passing.

Worth flagging honestly: `param_counted_of`'s header reasons about a caller
whose release is "a shallow arr_dec, not the deep element free". This consumer's
release IS the deep walk, so the tier is being used past the letter of its own
justification. The two rules above are why that holds; they are not this slice's
invention, but stating them is its obligation.

The case was rewritten rather than deleted, and a harder one added beside it in
all three suites: `strarr-local-callee-holder-escapes`, where `build` RETURNS the
Box so the retain is still live when `xs` sweeps. Measured — the walk runs, finds
rc 2, and decs without touching an element — and every element is read back after
20 churn frames have recycled the freelist. That case can fail; the one it
replaces could only ever fail on shape.

Three shapes measured, all three oracles agreeing on each: holder local
(9000/9000 clean), holder returned (14500/14500 clean where native leaks), and
50 holders retained in a container across 200 churn frames (correct answer,
self-host leaks — the leak-safe direction).

## Probes

`scratchpad/rc/pm_*.fern` (the five cells, regenerated from the matrix template
with the exact `mkv(7)` shape the matrix uses) and `cn1`–`cn5`, `cn1b` — the
escape paths, each reading its value back BY BYTES after 200 churn allocations,
because the census cannot see a use-after-free.

An `own` param cannot be probed from a plain local: E051 refuses the call.

## Gate selection was the actual mistake

`docs/TEST-GATES.md` rule 13 says to pick the gate by the fixture's SHAPE, not
by the test's name, and gives a grep recipe. This slice ignored it, ran a curated
`-run` regex, and got a green that three backends disagreed with. The local wasm
sweep is what caught it — not the targeted set.

Applying the recipe afterwards (files declaring a struct with a `string` field
AND pinning `frees` / `__rc_underflow`) selects **70 files, 135 tests, 237 s**,
and it reaches every suite this change moves. That is the gate this class of
change wants; a hand-picked regex is a gate you chose, and it will agree with you.

Run: the fixpoint (94 s, unchanged against #7591's 93 s — the extra registry
lookup costs nothing measurable, and it is reached at most once per walk because
the walk bails immediately after), the shape-selected 135 (237 s), the widened
targeted rc set (168 s), every `TestSelfHostStrArr` across all three backends
(76 s), and the full wasm sweep — 521 tests, zero failures, zero skips (622 s).

## Matrix

`str__param` and `str_arr__param` re-pinned `clean clean`. The construction-retain
matrix now stands at **4 leaking cells of 35** — `str_arr__fieldread`,
`enum__param`, `enum_arr__param`, `struct_arr__param`.
