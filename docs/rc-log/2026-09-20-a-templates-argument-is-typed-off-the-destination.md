# 2026-09-20 — a template's argument is typed off the destination

After the synthesised-tag work the fuzz census had 33 programs left, and
two of the root leaves were one thing: a literal handed to a template.
`var m: Map[i32, i32] = id(Map {})` refused as `unsupported map shape: no
destination names one` (seeds 000, 075, 098 and four more), and `var fs:
((i32) => i32)[] = id([cw3_a, …])` as `unresolved array literal type`
(seeds 363, 381, 397).

`invoke_rest` produces each argument at its parameter's type under the
bindings the earlier arguments made, and for `id[T](x: T)` the first
argument's parameter is the bare variable. A literal has no type of its
own — an empty map or array literal has no element to name one, and an
array of lambdas is typed by what receives it — so `expr` reached
`map_construction` or `array` with nothing concrete and refused.

The destination knows: the binding's `Map[i32, i32]` names T through the
contract's RESULT. `destination_binding` binds the result against the
call's expected type once, and `invoke_rest` substitutes from it only for
an argument whose parameter is still unconcrete after the earlier
arguments' bindings. An argument naming its own type binds the variable
itself, left to right, as before; a destination the result cannot fit
binds nothing and the argument is refused as it was. The instance the call
requests is the one the argument's type names, so `id[i32[]]` is what
`id([])` under an `i32[]` binding produces.

Fifteen more programs produce whole, not three: the array literals were
the visible leaf, and a literal handed to a template sat behind the "is a
function value … builds" consequences of a dozen others. The map seeds
move one leaf deeper,
to a real limit: `id$$map$i32$i32: map unit is not shared`. The instance
returns its parameter, a map, and a map box carries no reference count on
the register backends — a unit of one is only ever moved
(`ssarc.shared_map_error`, `docs/SELFHOST-SEMANTIC-SOURCE.md`). `same(m:
Map[i32, i32]): Map[i32, i32] { return m; }` refuses the same way with no
template in sight, so that leaf is the map representation's, not this
change's.

## Census

| binary | whole | agree | diverge |
|---|---|---|---|
| main after #9825 | 476 / 509 | 500 | 0 |
| this change | 491 / 509 | 500 | 0 |

The compiler's own sources produce whole (8625 of 8625).
