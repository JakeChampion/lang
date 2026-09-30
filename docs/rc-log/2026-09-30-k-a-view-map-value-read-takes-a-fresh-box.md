# 2026-09-30 — a view map value read takes a fresh box (#10701)

Self-host (`ssarc`, the register runtimes' `__fern_str_own`).

On the register backends a `str` view is a 24-byte box with the immortal rc
-1, so a retain cannot count a second holder. Every read out of a `Map[_, str]`
value column retained the column's box: `get`'s payload, `get_or`'s answer,
`values()`'s per-element retain, and so `for (k, v) in m`, which walks the
value snapshot. The reader's release then freed the map's entry. #10698 made
the typed lowering refuse these reads ("a read of a view map value would share
the column's view box"), so under `FERN_SEM_IR_STRICT` they failed to compile.

## Fix

`__fern_str_own(s)` answers a string its holder owns a unit of: a counted
string retained, a fresh box over an arena view's bytes, and a view outside the
arena (a literal) as is. `__fern_arr_own_elems` (#10726) now calls it per
element instead of carrying the same code. wasm maps it to `$__fern_rc_inc`,
because a wasm slice is a counted block of its own bytes.

- `get_or` calls it in place of the retain.
- `get` rebuilds a hit around it: `opt_make(0)` over the owned payload, and
  the runtime's Option box freed alone.
- `values()` reads the column raw (flag 0) and owns each element through
  `__fern_arr_own_elems`, the same tail a window copy takes.
- `unshared_map`, the copy an insert or `without` makes of a shared map, owns
  each value it re-inserts the same way.

A view column no longer goes to core/map (`routed_box`). core/map retains a
box value on every read and on the copy-on-write an insert makes, and none of
those retains counts a view. Before this, a lent `Map[i32, str]` receiving an
insert double-freed its view boxes, with no read in the program. The runtime's
map is an association list, so lookups on a view map are linear. No
`Map[_, str]` exists in the compiler, stdlib or conformance sources.

## Measured

`every-read-of-a-view-map-value-is-a-fresh-view` (`get`, `get_or` hit and miss,
`values()`, iteration), with the refusal removed and no fix: x86-64 and arm64
printed `zzz` for later reads, and the sanitizer reported a use-after-free. wasm
answered correctly. With the fix, all four legs answer `13` with native's
output, and the sanitizer leg balances (31 allocations, 31 frees).

`a-copied-view-map-owns-its-boxes` (an insert and a `without` on a shared map,
an insert on a lent one, an overwrite, a counted string and a literal as values,
an `i64` key): 60 allocations, 60 frees. The AST lowering refuses it.

## Still open

`ssasem.copyable` still refuses a call's result holding a view map where a
copy would be needed, since `deep_copy` rebuilds no map.
