# The cursor idiom — immutable read-and-advance

Date: 2026-06-01. Status: decided; json.fern + stream.fern migrate to it.

## Problem

Two stdlib parsers/readers were built on a **mutable cursor**: a method
both *returns a value* and *advances a position* by mutating a struct
field in place.

- `std/json.fern` — `__JsonParser { s, pos, error }` threaded through 8
  mutually-recursive functions (`__json_p_skip_ws`, `_byte`, `_number`,
  `_uhex`, `_string`, `_array`, `_object`, `_value`); 32 `p.pos = …` /
  `p.error = …` mutations.
- `std/stream.fern` — `Stream { data, pos }`; `read_byte` / `read_n` /
  `read_line` return a value and bump `s.pos`.

Under immutable data structures a void/in-place mutator can't exist, and
— unlike the receiver-builder files (headers, io_buffered,
mock_platform) where the method returns *only* the new receiver — these
must return **both** a result and the advanced cursor. So the
`m = m.method(...)` rebind contract isn't enough on its own.

## Decision: return `(result, cursor)`, destructure at the call site

A function that consumes input returns a **tuple** of its result and the
advanced cursor. Callers `let (v, c) = f(c);` and thread the new cursor
forward. This is the single idiom for *both* files — solving the same
shape two different ways would itself be incorrect.

```fern
// before (mutable):
//   function (s: Stream) read_byte(): Option[i32] {
//       if (s.pos >= s.data.len()) { return None; }
//       var b = s.data[s.pos]; s.pos = s.pos + 1; return Some(b);
//   }
//   var b = s.read_byte();           // mutates s

// after (cursor idiom):
function stream_read_byte(s: Stream): (Option[i32], Stream) {
    if (s.pos >= s.data.len()) { return (None, s); }
    var b: i32 = s.data[s.pos] as i32;
    return (Some(b), Stream { ...s, pos: s.pos + 1 });
}
//   let (b, s) = stream_read_byte(s);   // rebinds s
```

Pure predicates that only *read* the cursor without advancing it
(`__json_p_byte`, peek-style helpers) keep returning a bare value and
take the cursor by value — no pair needed.

### Why this shape (vs. alternatives)

- **A `{result, cursor}` struct** (the headers/BytesWriter
  return-the-receiver shape extended) also works, but tuples +
  `let (a, b) =` are lighter and already first-class; a named struct per
  return type is boilerplate.
- **A mutable cursor with copy-on-write** would reintroduce the in-place
  mutation the migration removes.
- **Effect/state monad threading** — not expressible; out of scope.

Verified the idiom end-to-end before adopting (interp + native x86-64):
tuple return + `let (v, c) =` destructure, including through recursion
with `Cur { ...c, pos: … }` struct-update threading. (See the
feasibility probes in the session that produced this doc.)

## Error handling

The json parser used a sticky `p.error` flag. Under the idiom, fold the
error into the cursor (`Cur { s, pos, error }` stays — `error` is part of
the threaded state, set via struct-update like `pos`) OR surface it in
the result type. Keep `error` in the cursor for json: it's read after the
top-level parse to decide `Some`/`None`, and threading it in the cursor
avoids changing every helper's result type to `Result`.

## Migration shape (json.fern)

- The 8 `__json_p_*(p)` functions become `__json_p_*(p) -> (T, __JsonParser)`
  (value-producing) or `(__JsonParser)` (advance-only, e.g. skip_ws),
  with `__json_p_byte` staying a pure read.
- Every internal call site rebinds: `let (v, p) = __json_p_value(p);`.
- `json_parse` destructures the final `(value, p)` and inspects `p.error`.
- Mutual recursion (`value`→`array`/`object`→`value`) threads the cursor
  through each pair return — the recursion probe above confirms this
  works.

## Gating (both files are validated paths)

- `json.fern` is **self-compiled** (`internal/e2e/self_host_json_test.go`)
  → the byte-identical self-host fixpoint gate must stay green.
- json + stream have wasm e2e + interp + rc-correctness coverage →
  run under wasmtime locally (`/tmp/wt`, `FERN_WASI_ADAPTER`), not just
  the skip-when-absent default.

## Follow-ups

- After both migrate, the cursor idiom is the documented pattern for any
  future reader/parser; the convention-based Reader/Writer polymorphism
  note in stream.fern should reference this doc.
- A `let (a, b, c) = ` arity>2 form isn't needed here (all returns are
  2-tuples), but if a parser ever needs `(value, cursor, extra)` the
  destructure already supports N≥2.
