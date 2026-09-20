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

wasm keeps a SECOND body for the same builtin: a hand-written WAT
`$__fern_str_replace` in `wasm_ir.fern`, reached through the same op, with the
same two `return $h` early paths. The register backends' fix says nothing about
it, and the leak pins that caught the fault engage only on the sanitize leg, so
it stayed silent — 25 over-releases on the typed leg of a probe whose AST leg
reported none, counted by `__rc_underflow_count()`.

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
caller owns. `wasm_ir.str_replace_helper` gets the same pair: a WAT
`$__fern_str_dup` that copies through `$__fern_str_box`, called from both. Teaching the boundary that this one builtin may alias an argument
is not expressible: whether the result aliases is a run-time property of the
input, so no static contract covers both paths. An audit of every `rt_src_*`
helper that returns a `string` or an array found this the only one that returned
one of its own parameters — and that family is the register backends' emitting
helpers alone, which is why the wasm body needed finding separately.

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
| self-host, typed lowering, x86-64 | **segmentation fault** | 9, 0 B held |
| self-host, typed lowering, wasm32 | 9, **25 over-releases** | 9, 0 over-releases |

Corpus census (the conformance cases, coreutils, `examples/bench`,
`examples/cli`, `examples/tests` and the compiler; 865 seeds), both legs run
here with the same script:

| | before | after |
|---|---|---|
| programs produced whole | 811 of 865 | 819 of 865 |
| declarations produced | 81,410 of 87,203 | 83,189 of 87,203 |

Eight programs go from nothing to whole, and none regresses. Three are the ones
above; the other five reach the same two fixes through their own vocabulary —
`ordmap_test` (338), `pmap_test` (337), `pvec_test` (227), `bench_test` (212)
and the `closure_capture_shared_cell` conformance case (106).

## What gates it

- `TestSelfHostSemanticProduction/a-view-is-a-map-read-key`: a `Map[string, i32]`
  read through `slice_unchecked` windows, 50 of 50 produced on all four targets,
  `noLeak` on the sanitize leg. It reported `view element escapes its source`
  and produced nothing before.
- `TestSelfHostSemanticProduction/a-builtin-string-result-is-never-its-argument`:
  `replace` on the matching path and on BOTH early paths, with a temporary
  argument, printing `__rc_underflow_count()` rather than leaving the fault to
  the leak pins, which engage only on the sanitize leg. The relative pin is what
  catches it: the typed leg faulted on x86-64, and counted 25 over-releases on
  wasm, where the AST leg did neither.

## Traps

- **A view refusal is not one leaf.** The five programs the census charged to
  the view family had four different root spellings between them
  (`view element escapes its source`, `map value type`, `a view is lent, never
  retained`, and a use-after-free that is not a refusal at all). Reading the
  histogram's top line as one fix would have closed one program.
- **An audit of the shared helper sources misses a backend's own body.** The
  first audit here read every `rt_src_*` helper and concluded `replace` was the
  only one that returned a parameter. That was true and still incomplete: wasm
  hand-writes its own WAT for this builtin, outside the sources being audited.
  A builtin's implementations are per backend, so a per-builtin audit has to
  start from the op.
- **A leak pin that engages on one leg proves nothing about the others.** The
  sanitize leg is x86-64 only, so the wasm double release passed the same test
  that caught the x86-64 one. What makes it visible on every leg is putting the
  over-release count in the program's output.
- **The remaining two are a different problem.** `std/format` and
  `examples/cli/fold` bind a `str` assigned in more than one place, which makes
  a phi of views the planner has no anchor for. #9877 has the measurement,
  including why making every view phi unowned is wrong: `str.take` and
  `str.drop` answer an OWNED string, so such a phi can be the only holder of a
  fresh box.
