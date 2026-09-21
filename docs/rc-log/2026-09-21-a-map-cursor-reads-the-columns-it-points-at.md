# 2026-09-21 — a map cursor reads the columns it points at

`Map[K, V].iter` was the largest remaining typed-path refusal after the format
family: `unsupported call target: Map[string, JsonValue].iter`, 27 refusals in
`examples/tests/json_roundtrip_test` alone, and it held that file (244
declarations) and `conformance/cases/audit_std_json` (82) entirely to the AST
lowering.

## What the cursor is

Read off the emitters rather than inferred — `ir.fern`'s op cluster and
`asm_ir.fern`'s x86-64 arm:

- `map_iter` is the only allocation of the five: `__fern_alloc(16)`, a bare
  block holding `[map_ptr@0, cursor@8]`. **No rc header.**
- `mapiter_key` and `mapiter_value` are raw loads of `keys[cursor]` and
  `values[cursor]` — no retain. The element stays the map's.
- `mapiter_has_next` reads `cursor < keys.len`.
- `mapiter_advance` bumps the stored index in place and leaves a dummy.

So the cursor is an address the frame never counts, never retains and never
releases, and every read through it is a read of the map's own storage.

## The shape that made it fit

#9562 sized this as blocked: "the semantic path cannot describe the iterator as
a unit the frame owns while the box has no header to release: `ssarc` would emit
a release the runtime cannot service", and concluded the box needs an rc header
on all three backends first.

That is true of a contract that OWNS the cursor. This one lends it. The cursor
joins `unit_free` beside a stream handle, so no release is ever planned and the
missing header is not in the way. What it does need is the map kept alive, and
that is `ssasem.projects`: the cursor is a projection of its map, and `key` and
`value` are projections of the cursor, so the existing anchor chain keeps the
map live for exactly as long as any read through it can happen. #9562's own
closing note asks for that — "anchor the iterator to the map the way a string
view is anchored to its source" — and it is the machinery the view leaves
already built.

The one edit outside the ownership story is the checker's: `MapIter[K, V]`
resolved through the argless reserved-builtin arm and lost its arguments, so a
binding declared `MapIter[string, i32]` held a semantic value of a different
type. It resolves carrying its two arguments now, exactly as `Cell[T]` carries
its element and for the same reason — `key()` and `value()` answer those types,
and an argless spelling names no columns.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle.

| program | before | after | AST leg holds | typed holds |
|---|---|---|---|---|
| `examples/tests/json_roundtrip_test` | 0 of 244 | 244 of 244 | 182,512 B in 2921 blocks | 192 B in 12 |
| `conformance/cases/audit_std_json` | 0 of 82 | 82 of 82 | 7,200 B in 133 | 16 B in 1 |

Both answer identically on the typed path, the AST leg and native, and the
conformance case matches its `expected.stdout`.

The bytes the typed leg still holds are the cursor blocks themselves, one 16-byte
`__fern_alloc` per `iter()` — 12 cursors and 1 cursor respectively. That is
#9562, which this does not fix and does not worsen: the AST leg holds the same
blocks, and on the test program below both legs hold exactly 848 bytes for the
same 53 cursors.

On the corpus census (864 seeds, both legs against snapshot binaries built on
this change's own base) five files move and +329 is exactly their sum: 834
produced whole before and 839 after, 76,758 of 78,627 declarations before and
77,087 after. The census was run twice more after the escape refusals landed,
since each touched compiler sources; all 865 reports are byte-identical across
the two runs, so no corpus program returns a cursor and the refusal costs the
corpus nothing. Beyond the two above, three one-declaration conformance cases were
blocked on the same root — `map_iter_unannotated`, `map_str_iter` and
`map_struct_value_field`.

`conformance/cases/map_iter_struct_value` (70 declarations) does NOT come with
this one. Its cursor produces; the file still refuses on
`unsupported map shape: Map[Sku, Item]`, a struct-keyed map, which is the
`unsupported map shape` leaf and a different root.

## The shape the anchor does not cover

An anchor keeps the map live for as long as a read through the cursor can
happen INSIDE the frame that holds both. A returned cursor leaves that frame,
and the columns it points at are the ones the frame is about to release. So
`build` refuses a cursor result, exactly as it already refuses a view result
and for the same reason — `cursor result escapes its map`.

That refusal replaced a compiler CRASH: before it, `return m.iter()` from a
function declared `MapIter[K, V]` took the self-host down with `fern: array
index out of range` and a backtrace. A construct that does not lower is a
diagnostic, never a backtrace.

**A container carries the cursor exactly as far**, and the first cut of the
refusal read only the result's own type, so `MapIter[K, V][]` was produced —
two of two declarations, answering — while the bare form was refused. The
container is worse than the bare one: `array_new` is no projection, so the
array has no anchor edge to the map at all. The refusal walks the whole result
shape now — array element, tuple element, cell element, map key and value, and
a union's argument. A tuple holding a cursor is refused the same way, and is
the one form with no differential row, because the AST leg declines it outright
("module is not IR-eligible") and a differential row needs an oracle that
runs.

`Option[MapIter[K, V]]` was already refused before the union arm went in — but
as `missing nested record schema`, because the schema walk has no layout to
record for a cursor. Refused by the wrong gate is the same shape as the AST
leg answering "correct by leaking": right today, for a reason that is not the
rule. The union arm makes the refusal name its own reason.

## A test that could not fail

The container form first went into the bare form's row, where it proved
nothing. The `refuses` check is a substring match on the report, and the bare
refusal satisfies it on its own; `atLeast: 0` sets no floor; and the container
shape ANSWERS when it is wrongly produced, so the differential stays green too.
Deleting the container walk would have left every check in that row passing.

It is its own row now, and that was verified the only way worth trusting:
reverting `holds_map_iter` to the top-level `is_map_iter` turns the container
row RED on all four targets while the bare row stays green.

Native compiles the same program and answers 0 where the AST lowering answers
7: it reclaims the map at the producer's exit and the caller reads a freed
column header, which reports empty rather than faulting. The AST leg answers
only because `irlower`'s `map_has_explicit_iter` never reclaims a frame map
that has an explicit `iter()` — correct by leaking. One shape, two wrong
behaviours, and nothing in either checker refuses to write it: #9920.

## A branch that cannot run

`op_map_iter` spells widekinds for an 8-byte value column and the first cut of
`value_widekind` mirrored them. BOTH are unreachable, not just the float one:
`ssasem.narrow_map_value` takes an integer only when it is not wide, and
`counted_map_value`'s three shapes — a string, a string array, a box — are none
of them wide either, so an admitted map's value column is never 8 bytes. The
helper could only ever answer 0, so it is gone and the call sites pass 0 and
false with the reason recorded where they are.

Removing it changed the compiler binary, which invalidated a census already
half-run against the previous one. Restarted rather than reasoned about: a
census is a headline number, and "an unreachable branch cannot matter" is the
kind of claim that has been wrong twice in this log.

All thirteen local gates are green on the committed tree, the whole-compiler one
at 772 s. Eight subtests skip and none of them is this change's: seven are the
`os-floor-*` rows the harness skips on wasi, whose builtins that profile does
not grant, and the eighth is `Stage2FixpointArm64/self`, which the workflow
deliberately leaves behind `FERN_STAGE2_SELF=1`.

## Trap

**A refusal that names one thing can be several in a row.** The cursor took five
separate gates before it produced, each reported as a different sentence and
each needing its own answer: the call target, the binding's declared type
(the checker arm above), `unsupported record type` (the schema walk wanted a
field list the cursor has none of), `unsupported counted-unit type` and
`unsupported physical RC value type` (both type gates that admit a nominal only
by finding its record layout). Reading the first refusal as the size of the job
would have under-scoped it by four.
