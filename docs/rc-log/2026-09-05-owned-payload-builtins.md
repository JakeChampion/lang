# A match over read_chunk / read_line / read_file owns the payload it binds (#8396, #8399, #8403, #8408)

*2026-09-05* — native `internal/ir` plus the read_chunk runtime on x86-64,
arm64 and wasm; the self-host side measured and found leaking (#8402).

## The shape

```fern
while (true) {
    match (r.read_chunk(131072)) {
        Some(chunk) => { match (w.write(chunk)) { Some(_) => { return 1; }, None => {} } },
        None => { break; }
    }
}
```

`FERN_LEAKCHECK=1`, 20 MB of stdin, x86-64, before: `allocs=463 frees=0
live_bytes=20194976`. Every chunk of the streaming-reader idiom leaked, and
with it every line of a `read_line()` loop, every `env()` hit, every
`read_file` / `read_file_bytes` result bound by a match. `std/io.read_all_stdin`
carried the same shape.

## Why nothing released it

The helper hands back an IMMORTAL Option / Result box (`__fern_alloc_box`,
static-sentinel header — `rcResultImmortal`) whose success payload is a fresh
rc=1 string or u8[] built for this call. The box needs no release; the
payload's unit is the caller's, and nothing in the IR knew:
`reclaimableMatchScrutinee` admits user-declared callees only, and a
non-consuming match binding is borrow-tainted in `computeFreeEligible`, so
the exit sweep skipped it too.

## The rule

`rcOwnedPayloadBuiltins` (rcresults.go) names the six callee spellings whose
box is immortal and whose string / array payload is owned:
`__method_Reader_read_chunk`, `__method_Reader_read_line`, `read_line`,
`env`, `read_file`, `read_file_bytes` — each read off the runtime body on all
three backends (`__fern_alloc_rc1` / `__alloc_u8` / `__fern_str_copy`; the
preview-2 read_chunk copied into an rc1 block as part of this, see below).

A `match` whose scrutinee is a direct call to one is an
`ownedPayloadMatches` entry, and `computeConsumingOwnedMatches` admits its
qualifying arms' owned-payload bindings into the `consumingBindings` role
the consuming owned-param match (#4400) already has: pre-zeroed prologue
slot, deep-dropped by the exit sweep, moved on return. Two additions carry
the loop case the param match never had: the bind site drops the slot's
previous value before storing the new payload (`emitOwnedSlotDrop`), and a
`_` at an owned position drops the payload on the spot. No loop
restriction (each iteration reads a fresh box), no sibling poisoning (an
unadmitted binding is today's leak, not a stranded transfer). Guarded arms,
sub-patterns, `MatchExpr` and a wildcard arm keep today's leak. `if let`
and `let … else` are `*ast.Match` and come for free.

## What it measures

x86-64, 200 MB through the pass-through loop: 0.17 s → 0.030 s (`cat`:
0.034 s); `__heap_bump_bytes` per 100 iterations at 1 MiB: 131 MB → 4.8 KB
(the two immortal boxes per call, #8405). Conformance: `alloc_flat_read_chunk`
("constant" on x86-64 / arm64 / wasm, "scales" with reclamation off);
`TestX86_64/Arm64/WASMOwnedPayloadMatchReclaim` over the whole family plus an
escape-through-append probe read back after the loop.

## The runtime half (#8399)

Once the caller frees the chunk, `__fern_reader_read_chunk` itself was
wrong three ways: a short read (a pipe gives 64 KiB; wasmtime caps stdin
reads at 64 KiB too) freed the `n`-byte block at `length + 1` (x86-64) /
`length` (wasm) and stranded it below its class — `cat file | prog` bumped a
full chunk per iteration again; the EOF path leaked the buffer on every
backend; wasm bumped an rc1 scratch per call and the preview-2 body pointed
`Some` at a `cabi_realloc` list with no rc header. x86-64 and wasm now copy a
short read into an exact-size block and free the oversized one, every
backend frees on EOF, wasm frees its scratch and preview-2 copies the host
list into an rc1 block. arm64 frees at the size word `__fern_alloc_rc1`
writes and never had the strand.

## The bytes round trip (#8403)

`s.bytes()` → `string_from_bytes_unchecked` leaked all three 160 KB blocks per
call on x86-64: `bytes` is `__memcpy(out as usize, s.as_bytes() as usize, n)`
and the oracles read that as an escape. `syncByteCopyCall` /
`syncByteCopyRoots` state the one fact for `stringParamCounted` (the
source's occurrence is a non-retaining read), `computeFreshLocals` (a byte
copy into a scalar-element buffer does not end its freshness) and the
CastExpr escape taint; `exprNoParamEscape` admits `__alloc_u8` /
`random_bytes` / `tcp_recv` as fresh inits, and `freshOwnedRcTempType` plus
`copyingBuiltinArgs` admit `string_from_bytes_unchecked` and
`Writer.write` so the argument temps release. 200 MB: 0.7 s → 0.05 s.
Conformance: `alloc_flat_bytes_roundtrip`. The zero-copy retag of a unique
u8[] into a string was rejected: the two layouts put the block base at
`data-16` and `data-8`, so a retagged block frees at the wrong base.

## Traps

- **Under `-sanitize` nothing recycles** (the use-after-free quarantine), so
  a bump-growth probe built with it always reads "grows"; use
  `FERN_LEAKCHECK=1` alone for the shape and `-sanitize` for the verdict.
- **A per-call constant hides in a doubling test.** The immortal boxes cost
  24–48 B per call, so "twice the rounds must not cost more fresh bytes"
  fails even with every payload recycled. The two new cases compare the
  same round count at a 64x payload size instead, after one warm-up round
  at each size (the first allocation of a class is fresh however well it
  recycles) — the shape that separates the payload from the box.
- **A test's wide input must not multiply the CALL count.** The first
  family probe gave the wide file 64 lines, so `read_line` ran 64x more
  often and its box constant "scaled". Both fixture files hold one line.
- **wasm's `Writer.write` copied every string byte-by-byte into a raw
  block it never freed** (#8408): the pass-through loop leaked the whole
  input on wasm and masked half of this measurement. Heap-form strings now
  write in place; the inline spill (≤ 7 bytes) still bumps.
- **The wasm consumingBindings prologue zero was one word.** A two-word
  string slot needs two zeros, or wasmtime rejects the module ("expected i32
  but nothing on stack") and arm64 segfaults; the param-consuming match never
  admitted a two-word string, which is how the prologue went untested.

## Self-host (#8402)

`bin/fern-selfhost` on the same loop: `allocs=461 frees=0 live_bytes=20201064`,
and the same with the scrutinee hoisted into a typed `Option[string]` local
(`hoist_call_scrutinees`' output form): its opt-fresh registry admits user
producers, not the resource methods. Both new conformance cases print
"scales" through it, so the three self-host fixture legs list them against
#8402; the rows fail the moment the port lands.

## Still open

- #8405 — the I/O helpers' Option / Result boxes stay immortal: 24–48 B per
  call. Design: rc1 boxes consumed by the match, shallow-freed per arm.
- #8406 — `as_bytes()` bumps a raw 16-byte slice header per call.
- #8408's inline spill; the wasm read_line / read_file scratch blocks the
  same way; `__fern_reader_read_chunk`'s own arm64 dead single-word branch
  went, but `env` / `read_line` / `read_file` on arm64 still carry theirs.
