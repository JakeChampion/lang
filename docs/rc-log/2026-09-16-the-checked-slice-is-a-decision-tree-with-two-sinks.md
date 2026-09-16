# 2026-09-16 — the checked slice is a decision tree with two sinks

`s[a:b]` refused on the semantic path ("unsupported expression", 10 corpus
sites): `produce` had no `ast.ExprSlice` arm at all, so every module holding a
checked slice kept the AST lowering whole.

**The shape.** The window is admitted when `0 <= a <= b <= len` and neither end
lands inside a codepoint, and the expression answers `None` when it is not.
Every test that fails branches to one `None` block, so the whole check is a
decision tree with exactly two sinks, and the result joins as a single phi of
the `Some` of the view and that `None`. Only the join needs a phi; nothing
merges anywhere else. The length is read once and each bound evaluated once,
ahead of every test, so a bound with a side effect runs exactly as often as it
is written — which is what `open_slice_evaluates_base_once` is about. The
boundary test is interior-only, as the AST lowering's is: an end at 0 or at the
length is a boundary by construction, and anywhere else the byte there must not
be a continuation byte, which is two more four-block probes converging on the
success edge.

**The unit.** An `Option` of a reference is a nullable pointer, so the `Some`
declares no unit of its own and the slice's unit is what the frame releases —
the payload IS the view. That is the same anchoring `slice_unchecked` already
had, reached through a variant construction rather than directly, and it needed
no new rule in `ssaunits` or `ssarc`.

Slicing an ARRAY stays refused. It answers a bare view rather than an `Option`,
and the second reference to the source's buffer it hands back is a kind this
vocabulary does not have; the five corpus sites that remain are all of that
form, and they now refuse as "unsupported slice source" rather than under the
generic expression refusal.

## The checker bug this surfaced

`ssasem` requires a slice's result to be a view, and the self-host checker was
naming the payload an owned `string` — its own comment beside the code said
`Option[str]` and the line built `t_string()`. So the payload the checker named
and the value the op produces disagreed, and the arm could not be written until
that was settled.

The divergence was not confined to this boundary. Native rejects

```fern
match (s[0:3]) { Some(v) => { var w: string = v; return w.len(); }, ... }
```

with E003, because `v` is a borrowed view of `s`. The self-host compiled it
silently, and `w` then read as an owned string over storage `s` owns, so the
frame released bytes belonging to its source. E065 still fired on a view of a
local escaping through a return, because that analysis tracks match-arm
bindings rather than the type, so the hole was assignment and every other rule
that reads the payload type. #9472 records it; the fix is the one line, and
`TestSelfHostBuildGateX86_64` pins both directions — the rejection, and the
same payload bound at `str` still compiling.

## Corpus

Whole modules produced: 416 → 419 on this measurement; `string_slice`,
`zero_width_slice` and `audit_strings_arrays_maps` join, and nothing regresses.
`string_slice_option` still keeps the AST lowering, on `?` over an
`Option[str]` rather than on the slice.

## Pinned

`TestSelfHostSemanticSourceRC` gains six functions on all four targets under
the leak check: a slice of a borrowed parameter, one keeping a counted local
live across the read, one whose source is a temporary with no other use, and
three taking the `None` edge — a bound past the end, a bound inside an `é`, and
a loop slicing one byte at a time so a per-iteration leak has somewhere to
show.

The open forms needed their own: no module that produces whole writes one, so
`s[lo:]`, `s[:hi]` and `s[:]` were reaching the new `open_end` branch under no
gate at all. `open_window` writes all three against a base that PRINTS, and the
want holds exactly three lines, so a base evaluated twice fails the fixture
rather than agreeing on a number. That is what `open_slice_evaluates_base_once`
is for, and it cannot do the job yet: its array half keeps the whole module on
the AST lowering.

`TestSelfHostSemanticSourcePrint`'s golden carries the whole tree, which is the
readable record of the block structure above.
