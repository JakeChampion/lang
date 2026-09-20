# 2026-09-20 — a map read's key may be a view

After the capture cell closed, the corpus census's largest remaining leaf was
the `str` view family: five programs at 0 declarations produced, charged to a
handful of sites that hand a borrowed window somewhere the boundary will not
take one. Three of the five are closed here. The other two stand on a
different gap, now #9877.

## What was wrong

**A map READ's key was refused for an INSERT's reason.** `semsource.map_key_value`
rejected a view for every map operation alike. That is right for `insert`,
whose key joins the key column and is released when the map is released — the
map may outlive the bytes a view borrows. It is wrong for `get_or`, `has` and
`delete`: they hash the key and compare it, `operation_supplies` counts no unit
for it, and the boundary already has a retag for exactly that position, the one
a borrowed `string` parameter is offered. `coreutils/tsort` looks its edges up
by a window onto the line it just read, and one such lookup took the whole
program off the typed path.

**`__fern_str_replace` handed back its own argument (#9874).** On the empty-needle
and no-match paths the helper returned the haystack box rather than a fresh one.
Every other string-returning builtin hands back a box the caller owns, and both
lowerings' models read the result that way, so a produced caller released a unit
it never took. `std/cli`'s `__cli_sq` is `"'" + s.replace("'", "'\\''") + "'"`,
and a token with no quote in it takes the no-match path: the typed lowering
faulted with `fern-sanitizer: use-after-free (touched a quarantined block)`
where native and the AST lowering answered.

**Three producers handed a view to a container that outlives its source.**
`path_clean` appended a window onto `p` to the segment array; `__cli_long` and
`__cli_cluster` put a window onto `argv[i]` into the options map. The array case
is the refusal `docs/SELFHOST-SEMANTIC-SOURCE.md` already describes; the map
case reads as a value-column type mismatch, since a `str` is not the `string`
the column holds.

## The fix

`map_key_value` takes a `stored` flag: an INSERT's key keeps the refusal, a
READ's and a DELETE's take `lend(…, borrow_mode(), false)` — the same retag a
borrowed string parameter gets — before the type check.

`rt_src_str_replace` emits `__fern_str_dup` beside the replace body and copies
on both early returns, so a string-returning builtin always hands back a box the
caller owns. Teaching the boundary that this one builtin may alias an argument
is not expressible: whether the result aliases is a run-time property of the
input, so no static contract covers both paths. An audit of every `rt_src_*`
helper that returns a `string` or an array found this the only one that returned
one of its own parameters.

The three producers copy (`+ ""`), which is what the checker rule #8635 stages
will demand of them.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle. The
typed and AST legs answer identically on every row, and match native.

| program | before | after | typed held | AST held |
|---|---|---|---|---|
| `examples/tests/cli_test` | 0 of 239 | 239 of 239 | 0 B | 34,344 B |
| `examples/tests/path_test` | 0 of 185 | 185 of 185 | 0 B | 4,704 B |
| `coreutils/tsort` | 0 of 135 | 135 of 135 | 0 B | 1,464 B |

The AST lowering's figures are #9832's first half, unchanged by this entry.

The `replace` fault on its own, with the argument a temporary nobody else names
(a second name on the box absorbs the extra release and hides it):

| leg | before | after |
|---|---|---|
| native, interpreted and compiled | 9 | 9 |
| self-host, AST lowering | 9 | 9 |
| self-host, typed lowering | **segmentation fault** | 9, 0 B held |

## What gates it

- `TestSelfHostSemanticProduction/a-view-is-a-map-read-key`: a `Map[string, i32]`
  read through `slice_unchecked` windows, 50 of 50 produced on all four targets,
  `noLeak` on the sanitize leg. It reported `view element escapes its source`
  and produced nothing before.
- `TestSelfHostSemanticProduction/a-builtin-string-result-is-never-its-argument`:
  `replace` on both the matching and the no-match path with a temporary
  argument. The relative pin is what catches this one — the typed leg faulted
  where the AST leg answered.

## Traps

- **A view refusal is not one leaf.** The five programs the census charged to
  the view family had four different root spellings between them
  (`view element escapes its source`, `map value type`, `a view is lent, never
  retained`, and a use-after-free that is not a refusal at all). Reading the
  histogram's top line as one fix would have closed one program.
- **The remaining two are a different problem.** `std/format` and
  `examples/cli/fold` bind a `str` assigned in more than one place, which makes
  a phi of views the planner has no anchor for. #9877 has the measurement,
  including why making every view phi unowned is wrong: `str.take` and
  `str.drop` answer an OWNED string, so such a phi can be the only holder of a
  fresh box.
