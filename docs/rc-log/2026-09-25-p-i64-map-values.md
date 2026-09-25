# A map of i64 values takes the typed path

Under `FERN_SEM_IR_STRICT`, `TestSelfHostMapColumnSnapshot*`'s
`i64-values-stay-raw` row was refused: "unsupported map shape: Map[i32,
i64]". The typed path admitted a value column of narrow integers, strings,
string arrays and boxes, not a wide one.

The IR already carried the width: `op_map_set`, `op_map_get`,
`op_map_get_or`, `op_map_values`, `op_map_iter` and `op_mapiter_value` all
take a widekind, and ssarc passed 0 to each.

- `ssasem.wide_map_value` admits an `i64` or `u64` value column. `f64` stays
  out: wasm has no helpers for it.
- ssarc passes `map_widekind` to every one of those ops. The register
  backends ignore it, since every cell is eight bytes there. Wasm routes the
  ops to its `_w64` helpers, which keep each value in a cell of its own.
- A wide column's `values()` is a snapshot the frame owns, as an i32
  column's is (`ssasem.retained_column`). Both register runtimes copy whole
  8-byte cells, and wasm builds a fresh `i64[]`.
- The copy an insert into a shared map makes reads the value snapshot with
  the i64 array op, into a scratch slot declared i64 (`i64_slots`).

## A wasm runtime bug on the way (#10280)

On wasm, `$__fern_map_values_w64` walked the slots in probe order.
`$__fern_map_snapshot`, behind `keys()`, copies in insertion order through
the `ord` column. So an i64-valued map's `values()` and iteration paired each
key with another key's value. The typed path's copy-on-write copy did the
same. It now walks `ord` as the key snapshot does.

## Tests

`TestSelfHostSemanticProduction` rows, on x86-64, x86-64 with the sanitizer,
arm64 and wasm:

- `a-map-of-i64-values-takes-the-typed-path` covers insert, overwrite, `get`,
  `get_or`, `values`, iteration, `without`, and an insert into a shared map,
  with no leak. The AST lowering refuses this program.
- `an-i64-map-pairs-each-key-with-its-value` checks every pair through
  iteration and through `keys()` against `values()`. Before the fix it failed
  on wasm.
