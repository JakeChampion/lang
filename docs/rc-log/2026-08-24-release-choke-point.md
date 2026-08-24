# The release choke points — slice 2 of promotion step 1

Companion to the retain slice (`2026-08-24-retain-choke-point.md`); together
they are step 1's emitters. `LowerState` gains `release(slot, freefn, site)`
(load / free / drop, net-zero), `release_zero(slot, freefn, site)` (the same
plus the slot-zeroing disjointness protocol, explicit in the name), and
`release_tos(freefn, site)` (free the stack top; the consumer or the
statement's own drop follows).

`freefn` stays a parameter because the symbol choice is memory-layout-
semantic, not stylistic — `__fern_rc_dec` frees an array at data-16 where a
string box's block starts at value-8, so the wrong helper is heap
corruption, not a leak — and the SITE knows the type. Guard structure (cow,
null, is_unique, tag dispatch) is never owned by the emitters: a guarded
site keeps its guards and routes only the emission.

## What routed (33 emissions)

The mechanical inline clusters: the whole discarded-expression-statement
reclaim ladder in `lower_stmt_inner` (arr/arrarr/strarr/tuple/struct
literals, concat, ARR:/ARROWN:/MAPRET:/TUPRET:/str-fresh-ret calls,
including the dynamic `__struct_drop_<T>` deep-drops), the concat operand
frees, the stashed literal-arg drains, the map-get key temp and `.len()`
fresh-producer temps, the try `?` fresh-box frees, the SCENRB rebind
release, the six map-iteration snapshot-column releases (release_zero), and
the precise-drop bare scalar-array (release_zero).

Still direct by design: the ~50 dedicated deep-free/reclaim-store helpers'
internals (already choke-point-shaped; they gain site keys when step 2
needs their verdicts), the exit sweep's is_unique-gated element walks, the
reuse emitters' arms, and every load-bearing guard nest.

## Proof

Old-vs-new byte identity: drivers built from the pre-slice and post-slice
sources ran per-module emit-all over the same pre-slice snapshot — 45/45
units byte-identical. The standing fixpoints cannot prove old == new (they
are self-referential), which is why the harness compares two differently-
sourced drivers on one fixed input.
