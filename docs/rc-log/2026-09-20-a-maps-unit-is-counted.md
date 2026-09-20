# 2026-09-20 — a map's unit is counted

After the signature work the fuzz census had eight programs left, all of
them `map unit is not shared`: a template handing its map parameter back
(`id$$map$i32$i32`), `pick(c, m, n)` choosing between two maps, and a map
read from two bindings. The rule was physical RC's one reading of the plan
rather than the graph: a map box on the register backends was the raw
sixteen-byte `{keys, vals}` pair `__fern_alloc` hands out, with no count in
it, so a plan that RETAINED one was refused and every unit had to move.

The wasm box has carried a count all along — `__fern_map_new_k` takes it
from `__fern_str_box`, and `$__fern_map_release` decrements a shared box
and frees the columns only for the last unit. The register backends now
match: `__fern_map_new` takes the box from `__fern_arr_box` with two
words, so the array header sits in front of it and `__fern_rc_inc`,
`__fn___fern_arr_dec` and `__fn___fern_rc_is_unique` read it as they read
any box. The free family — the plain, `_ks`, `_vs`, `_kvs`, `_vsa`, `_ksvsa`,
`_va`, `_ksva`, `_vf` and `_ksvf` members on x86-64 and arm64 — releases
ONE unit: a box above 1 is handed to `__fn___fern_arr_dec` for the
decrement (and the underflow count of a dead box) and left whole; the last
unit walks the columns as before and hands the block to the same helper,
which returns it to its class and, under `FERN_SANITIZE`, quarantines it.
The hand-rolled push onto freelist class 2 goes.

The AST lowering is unchanged by this: it never retains a map, its alias
group (#7235) decides ownership statically, so every box it releases is at
1 and the walk runs as it did. The semantic lowering drops
`shared_map_error`; a retain on a map is the ordinary `__fern_rc_inc`, and
`release_name` still names the free family, which now releases by count.

Two production cases pin it on all four targets with the sanitizer leg's
absolute leak pin: a map handed back by a template and by a plain function
and read through three bindings, and a string-keyed, string-valued map
chosen between two by a function, whose columns must be released exactly
once.

## A shared map is copied before it is written

With a unit that can be shared, the fixture corpus's `cow_alias_safety`
came back 81 for 129: `var snapshot = m; m = m.insert(1, 99)` wrote the
box both bindings held. The AST lowering never meets this because its
alias group refuses the shape; the semantic lowering's `insert` and
`without` consumed their receiver's unit and wrote in place whatever the
count said. `ssarc.unshared_map` now gates every consuming mutation on
`__fern_rc_is_unique`: a sole-held box is written as before, and a shared
one is copied first — `map_new` at the receiver's length and one insert
per entry over plain snapshots of the two columns, each entry's units
supplied as the typed insert takes them, since the register runtimes
retain nothing on an insert. The snapshots are released shallow and the
shared box loses this frame's unit, which cannot be its last.

Two shapes were tried and put back. Holding the receiver's retain back the
way `deferred_retain` does for an array append — so the gate reads the
count without this frame's unit and writes a lent map in place — matches
what native answers for `grown(m, 7)` through a lent parameter (43), but
native's answer is the bug: E055 says every collection operation returns a
new value, and native's rc pass borrows a map mutator's receiver, so
`var n = m.insert(k, v)` changes `m` on the native compiler, the
interpreter and the AST lowering alike (#9834). The semantic lowering keeps
the retain: a map the frame still reads counts as shared and is copied,
and `a-map-the-frame-still-reads-is-not-written` pins the contract's
answer (85) beside the AST lowering's (63, `astAnswers`), so the row is
retired with the fix. The other half of that 63 is the AST lowering's own:
`without` on an aliased receiver writes the shared box in place where
native copies (#9835).

## Census

| binary | whole | agree | diverge |
|---|---|---|---|
| the signature leaf (#9831) | 501 / 509 | 500 | 0 |
| a map's unit is counted | 509 / 509 | 500 | 0 |
| a shared map is copied before it is written | 509 / 509 | 500 | 0 |

Every program the corpus holds is produced whole, and the AST lowering
refuses exactly the nine it refused before. No corpus program reads a map
it has mutated, so the copy-on-write gate changes no answer there; the
production rows above are what pin it. The compiler's own sources
produce whole (8640 of 8640).
