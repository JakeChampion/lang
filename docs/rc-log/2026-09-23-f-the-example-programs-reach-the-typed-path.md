# The example programs reach the typed path

2026-09-23. With the corpus and the compiler's own source whole on the typed
path, the next census is the repository's other programs: the 549 under
`examples/` that are not the compiler. 542 produced whole. Five of the other
seven fell to three gaps, closed here.

- **A function type's signature records.** A record holding a function value
  names the records that function takes and returns:
  `struct Stage { run: (Ctx) => Result[Ctx, Fault] }`. `semrecords` requires
  their schemas, but `schema_of` never walked into a function type. So any
  body that named only `Stage` failed verification with "missing nested
  record schema" (`examples/proposals/pipeline.fern`, and
  `examples/vcl/vclrun.fern`, `vclbackend_test.fern` and `vclproxy.fern`).
- **A literal beside an operand of unknown width.** When the checker gives
  neither side of an operator a width, an unsuffixed literal is now read at
  the other side's integer type, whichever side it is on. `std/tcp`'s
  `(deadline_ns - (monotonic_ns() - read_start_ns)) / 1000000` was refused
  as `i64 / i32`.
- **`tcp_pollable`** had no contract (`examples/vcl/origin.fern`,
  `vclproxy.fern`).

All five now produce whole: pipeline 109 of 109, vclrun 449, vclbackend_test
545, vclproxy 556, origin 144.

Two programs still refuse, for reasons that are not gaps in lowering:

- `examples/wasm/word_freq.fern` stores a view in an array ("view element
  escapes its source"). That is O2's escaping position
  (docs/STR-VIEW-CONTRACT.md §5, #8635).
- `examples/cli/fold.fern` merges views of two sources into one value.

Production rows:

- `a-function-field-names-its-signature-records`
- `a-literal-takes-its-operands-width`
- the `os-floor-sockets-and-ids` row now calls `tcp_pollable`
