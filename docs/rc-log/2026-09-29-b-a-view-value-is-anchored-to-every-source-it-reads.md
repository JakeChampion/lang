# A view value is anchored to every source it reads

A value that gathers views was anchored to the one source those views shared
(`ssasem.bytes_root`). A value reading two sources had no single anchor, and
its function was refused ("a value holds views of two sources", #10687). A
view result reading either of two parameters was copied instead
(`2026-09-25-x-a-view-result-with-no-one-source-is-a-copy.md`).

Both now keep every source alive.

- **Local values:** `bytes_roots` returns every source a value reads.
  `Analysis` keeps the direct `parents` beside their transitive closure
  (`ssasem.closure`), so each source stays alive until the value's last read.
- **Results:** `ssasem.Anchor` names every parameter a result reads, and a
  caller keeps each of those arguments alive while the result lives. A result
  that reads several parameters is no longer copied, so `either` from the
  2026-09-25 entry drops from 5 allocations to 4. `copy_returned_views` now
  copies only a plain `str` result whose view reads storage that is not a
  parameter's.
- **Containers and parameters:** a result holding views inside a container
  (`str[]`, a tuple) is no longer refused. A view parameter that is stored, as
  an element, through append or with, in a field, a payload or a map value,
  or that is returned, gets a fresh view of the same bytes
  (`semsource.kept_view`). A parameter is lent, so the stored view cannot be
  the caller's box.
- **Moves and grows:** `first_hop` returns -1 when a path passes through a
  value with more than one anchor. That only declines a move or an in-place
  grow.

## Cost

- One view box per stored or returned view parameter.
- The liveness of each extra source, which now lasts until the value's last
  read.

## Traps

- With one of two anchors kept, `a-value-holding-views-of-two-sources-is-anchored-to-both`
  prints junk bytes on x86-64, but the sanitize leg did not flag it. The
  answer check is the witness here, not the sanitizer.
- `ssasem.view_roots` takes its parent table borrowed. Taking it `own` hit
  #10718, a double free in the AST lowering on `return f(xs)` for a local
  nested array at an `own` position, and it broke the whole-compiler
  self-compile.

## Still refused

Reading a view map value back (`get`, `get_or`, `values()`, iteration) would
share the column's view box (#10701). It is now the one refusal the strict-IR
fixtures pin.

## Tests

- `TestSelfHostSemanticProduction`, on x86-64 (with the sanitizer too), arm64
  and wasm: `a-value-holding-views-of-two-sources-is-anchored-to-both` and
  `a-kept-view-parameter-is-a-fresh-view`.
- `strictIRCorpus`: `views-of-two-sources` and `local-view-held-twice` now
  compile and match the interpreter.
