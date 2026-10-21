# The cursor idiom — immutable read-and-advance

Status: [policy] decided; stream.fern uses it. std/json's parser used it too
until #12262 moved it to one shared position cell (below).
Date: 2026-06-01.

## Problem

Two stdlib parsers/readers were built on a **mutable cursor**: a method
both *returns a value* and *advances a position* by mutating a struct
field in place.

- `std/json.fern` — a parser struct `{ s, pos, error }` threaded through
  eight mutually-recursive functions, with 32 `p.pos = …` / `p.error = …`
  mutations.
- `std/stream.fern` — `Stream { data, pos }`; `read_byte` / `read_n` /
  `read_line` return a value and bump `s.pos`.

Under immutable data structures a void/in-place mutator can't exist, and
— unlike the receiver-builder files (headers, io_buffered) where the
method returns *only* the new receiver — these
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
//       let b = s.data[s.pos]; s.pos = s.pos + 1; return Some(b);
//   }
//   let b = s.read_byte();           // mutates s

// after (cursor idiom):
function stream_read_byte(s: Stream): (Option[i32], Stream) {
    if (s.pos >= s.data.len()) { return (None, s); }
    let b: i32 = s.data[s.pos] as i32;
    return (Some(b), Stream { ...s, pos: s.pos + 1 });
}
//   let (b, s) = stream_read_byte(s);   // rebinds s
```

Pure predicates that only *read* the cursor without advancing it
(peek-style helpers) keep returning a bare value and take the cursor by
value — no pair needed.

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

## std/json: one shared position cell

std/json's parser threaded `(value, cursor)` pairs through its recursive
descent, with the sticky error flag carried in the cursor. #12262 replaced
that with one `Cell[i32]` the whole parse shares: each function reads the
position from it and writes back where it stopped, and a failure stores
`-1 - offset`, which the first failure wins. A recursive descent that
returns a value at every level has no pair to build, so the cell is the
lighter shape there; a reader whose caller holds the cursor between calls,
as stream.fern's does, keeps the idiom above.

## Gating

- `json.fern` is **self-compiled** (`internal/testing/e2e/self_host_json_test.go`)
  → the byte-identical self-host fixpoint gate must stay green.
- json + stream have wasm e2e + interp + rc-correctness coverage →
  run under wasmtime locally (`/tmp/wt`, `FERN_WASI_ADAPTER`), not just
  the skip-when-absent default.

## Follow-ups

- The convention-based Reader/Writer polymorphism note in stream.fern
  should reference this doc.
- A `let (a, b, c) = ` arity>2 form isn't needed here (all returns are
  2-tuples), but if a parser ever needs `(value, cursor, extra)` the
  destructure already supports N≥2.
- Tuple-destructuring parameters shipped (#4406): a helper whose first
  move is unpacking an incoming pair can take it destructured directly —
  `function step((v, c): (T, Cur)): (T, Cur) { … }` — instead of a named
  param + `let (v, c) = p;`.
- The self-host parser adopted the idiom wholesale (#4406): all 22 of
  parser.fern's one-off `*Result { par: Par, … }` structs are gone —
  every `parse_*` returns `(value…, Par)` and call sites destructure
  `let (v, p2) = parse_x(p); p = p2;`. Multi-value
  results are N-tuples with the cursor last. The two mutable-accumulator
  loops (the precedence climber, the chained-assign collector) keep a
  plain mutable value local instead of rebuilding a result struct.
