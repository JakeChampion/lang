# 2026-09-21 — a string literal is already a view

`std/format` was the largest remaining typed-path refusal, and both of its
refused functions are built on the same four lines:

```fern
var spec: str = "";
…
if (j < n) { spec = slice_unchecked(fmt, i + 1, j); }
```

`__format_apply_spec` opens with `var fill: str = " ";` and rebinds it from
`slice_unchecked(spec, p, p + fw)` — the same shape, the same refusal:
`a view is lent, never retained`. Between them they held three whole
programs to the AST lowering: `examples/tests/format_test` (238 declarations),
`conformance/cases/format_specs` (136) and `conformance/cases/format_placeholders`
(130). Every other refusal in those three files is contagion from these two.

## Why the planner was right to refuse it

`semsource.produce` gave every string literal `t_string()`, and `widen` then
retagged it for the view destination with `str_as`. `ssaunits.projection` reads
a retag as a borrow, so the literal's unit stayed with the constant and the
retag held nothing.

The phi merging that retag with the loop's `slice` is owned — the slice edge
allocates a view box the frame must free — so each edge has to supply a unit,
and on the borrowed edge the only supply available is a RETAIN. A view's box
carries the immortal sentinel rather than a count, so that retain is a no-op
while the release balancing it frees: exactly the shape #9802 refuses.
Producing the function would have freed the constant's box from under the phi.

## Why the retag was never needed

`str_as` is an identity op — `ssarc.identity_op` says so in as many words:
"a view and the string it borrows are one box, so … a string retag move[s] no
bytes". A literal is `.rodata` with the rc word at `[ptr-8]` set to
`0x80000000`, which is the same immortal marking a view box carries, and
`__fern_str_view_free`'s immortal case guards on the box base being inside the
arena, so releasing a literal through the view helper is the no-op releasing it
through `__fern_str_free` already was.

So a literal at a `str` destination needs no conversion, no retag and no
borrow: it needs the destination's spelling. `produce` gives it that now, the
phi's entry edge is a MOVE, and the planner has nothing to object to.
`ssasem`'s constant rule widens from `is_string` to `is_text` to match — a
string constant is a legal value at either string type.

The retag stays for every other `string` in a `str` slot, where the operand is
a value with a count and a borrow is the right reading.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle.

| program | before | after | AST leg holds | typed holds |
|---|---|---|---|---|
| the two shapes plus a literal argument and a `str[]` of literals | 0 of 4 | 4 of 4 | 3240 B in 135 blocks | 0 B |
| `examples/tests/format_test` | 0 of 238 | 238 of 238 | — | 0 B |
| `conformance/cases/format_specs` | 0 of 136 | 136 of 136 | — | 0 B |
| `conformance/cases/format_placeholders` | 0 of 130 | 130 of 130 | — | 0 B |

All three format programs answer identically on the typed path, the AST leg and
native, and the two conformance cases match their `expected.stdout`. The
compiler's own sources go from 8755 of 8755 to 8756 of 8756 — the one added
declaration is the helper this change introduces.

## What this does not reach

`examples/cli/fold`'s `fold_line` still refuses on the same rule: its
`var rest: str = line;` is a retag of a borrowed `string` PARAMETER, rebound in
a loop, and a borrow has no unit to move. Closing that one means making the
retag a rename outright — the operand's unit becomes the retag's, so the edge
retains the COUNTED string rather than the view — which is a change to
`ssaunits`' ownership model. 74 declarations, and the next step of this story.

On the corpus census (864 seeds, both legs run against snapshot binaries built
on this change's own base) the three format programs are the only files that
move: 831 produced whole before and 834 after, 76,254 of 78,627 declarations
before and 76,758 after. +504 is exactly 238 + 136 + 130.

## Trap

**The AST leg is not turned off by `FERN_SEM_IR_SKIP=1`.** That variable is a
declaration-name PREFIX LIST, so `1` matches nothing and the typed path stays
on; a three-way comparison built on it compares the typed leg with itself and
agrees for the wrong reason. `FERN_SEM_IR=` (empty) is the off switch, and it
is what turned the 0-byte reading above into the real 3240.

## Adjacent, not fixed here

Writing the test for a literal in every `str` position turned up #9915: the
parser erases `str` to `string` at the parse boundary, and only `StmtVar` and
`ParamDecl` carry the `is_str` sidecar that puts it back, so a struct FIELD or a
tuple ELEMENT declared `str` is a `string` to the whole checker. It shows as an
E043 that fires in the opposite direction from native's, and as
`var p: (str, i32) = ("abc", 4); p.0.len() + p.1` answering 60 where native
answers 7 with no diagnostic on any leg. Both are outside this change: the typed
path already refuses the tuple shape, and a field sidecar is a parser + AST +
checker change of its own.
