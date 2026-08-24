# The construction-retain matrix — the killer-drops wave gets its map

The struct routing recorded a "killer-drops-fields release-protocol port"
as the wave that retires the sink refusals, the escape-channel conjuncts,
and the NODEEP role conventions. Reading the release helpers reframed what
that port actually is: the field decs are already rc-GUARDED
(`__fern_arr_dec` / `__fern_str_free` / `__fern_str_arr_free` all
dec-or-free by rc), so the unsound cases are not blind walks — they are
UNCOUNTED SHARES, decs with no matching inc. The flatten__RewriteCtx wasm
fault was exactly that: a string[] field READ reached the new box with no
retain, and the guarded dec was one claim too many per iteration. The wave
is therefore a retain-side completion — count every share, and the
existing guarded decs become sound by arithmetic — and its first slice is
the instrument that maps the holes.

## The matrix

`TestSelfHostConstructionRetainMatrixX86_64`: struct-literal FIELD KIND
(string, string[], i32[], nested struct, enum, enum[], struct[]) x VALUE
SHAPE (fresh, bare local, bare param, field read, call result), each cell
compiled under both compilers with FERN_LEAKCHECK, exits required to
match, the underflow guard failing hard, verdicts pinned in
`testdata/selfhost-construction-retain-matrix.txt`.

Measured: native is CLEAN on all 35 cells; the self-host matches every
exit with ZERO underflow and zero crashes, and diverges leak-direction on
17 cells. The holes, by axis:

- **fieldread leaks on every kind** — even the scalar-array field, whose
  bare-ident dup (#3292) works: the field-ACCESS spelling reaches the new
  box uncounted across the board. The RewriteCtx class, generalized.
- **local / param aliases leak** except `arr_i32` (the #3292 dup, both
  origins) and `struct__param` (the nested-struct arm's conservative inc
  with nothing on the param side to strand).
- **the enum kinds leak even fresh/call** — `enum_arr__fresh` leaks a
  freshly-constructed `[E.A(..)]` field, so the enum-array field admission
  is missing outright, not just the alias counting.

Retire order the table suggests: the fieldread column (one arm, every
kind), then the enum-array admission, then the local/param alias dups —
each slice flips recorded cells deliberately and the underflow guard
polices the direction.
