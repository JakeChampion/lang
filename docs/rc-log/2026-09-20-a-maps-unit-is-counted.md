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

## Census

| binary | whole | agree | diverge |
|---|---|---|---|
| the signature leaf (#9831) | 501 / 509 | 500 | 0 |
| this change | 509 / 509 | 500 | 0 |

Every program the corpus holds is produced whole, and the AST lowering
refuses exactly the nine it refused before. The compiler's own sources
produce whole (8638 of 8638).
