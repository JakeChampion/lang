# The I/O helpers' per-call Option / Result box is counted, so a streaming loop stops bumping one a call (#8398, #8405)

*2026-09-07* — native `internal/ir` plus all three runtime emitters
(`internal/codegen/{x86_64,arm64,wasmbin}`; `arm64ssa` already built these
boxes at rc=1 and needed no runtime change). Native-side only; the self-host
still hands back immortal boxes — see "What is left".

## The measurement

Two reproducers, both from the issues. `FERN_LEAKCHECK=1` builds, output
byte-identical before and after on every backend.

**#8398** — three `stdout()` and two `w.write(...)`:

| | allocs | frees | live_bytes |
| --- | --- | --- | --- |
| x86-64, before | 5 | **0** | 128 |
| x86-64, after | 5 | 2 | **96** |
| arm64, before | 5 | **0** | 80 |
| arm64, after | 5 | 2 | **48** |
| arm64ssa, before | 5 | **0** | 160 |
| arm64ssa, after | 5 | 2 | **96** |
| wasm32-wasi, before | 9 | **0** | 144 |
| wasm32-wasi, after | 9 | 2 | **112** |

**#8405** — `match (r.read_chunk(4096))` → `w.write(chunk)` over 400 000 bytes
of stdin (99 iterations):

| | allocs | frees | live_bytes |
| --- | --- | --- | --- |
| x86-64, before | 299 | 100 | 4 800 |
| x86-64, after | 299 | **297** | **64** |
| arm64, before | 298 | 99 | 4 768 |
| arm64, after | 298 | **296** | **32** |
| wasm32-wasi, before | 497 | 200 | 6 336 |
| wasm32-wasi, after | 497 | **397** | **1 600** |

The residue on the natives is the `stdout()` / `stdin()` HANDLE, which is
bounded per program rather than per call: 32 B a handle on x86-64, 16 B on
arm64. Nothing per-iteration is left there. Wasm keeps a per-iteration
16 B — exactly `100 x 16` — and it is NOT the box: it is
`__fern_writer_write`'s iovec scratch, a pre-existing leak filed as #8808.

The wasm leg was measured with node's `node:wasi`
(`--experimental-wasi-unstable-preview1`), which runs a `-emit command-module`
build and reports the same census line; wasmtime was not installable on the
machine this landed from.

## What changed

1. **Every per-call Option / Result box now comes from `__fern_alloc_rc1`**
   rather than `__fern_alloc_box`, so its header is a live rc=1 instead of the
   `0x80000000` sentinel every rc helper short-circuits on. 46 sites on arm64,
   37 on x86-64, ~40 on wasm.
2. **A payloadless arm allocates the enum's UNIFORM box size**, not the tag
   alone. This is the half that would have corrupted the heap rather than
   merely leaked: `__fern_writer_write`'s `None` was `__fern_alloc_box(4)` — a
   16-byte block — while the IR frees an `Option[IoError]` at 16 bytes of
   payload, i.e. `__fern_free(base, 24)`, which bins into the 32-byte class. A
   short block pushed onto a class its extent does not cover is handed out to a
   later 32-byte request. Nine such arms: `env`, `read_line`,
   `Reader.read_line`, `Writer.write` and `close` on the natives (arm64 needs
   24 rather than 16 where the two-word string ABI widens `Option[string]`),
   and their five wasm twins plus `reader_read_line_fd`.
   `arm64ssa`'s `emitOptionBox` already carried the comment that says why.
3. **`rcresults.go`** moves the family from `rcResultImmortal` to
   `rcResultOwned`. `helperAllocBoxCallers` in the wasm backend — the second
   record the classification is checked against — becomes
   `helperResultBoxCallers` and pulls `__fern_alloc_rc1` into the helper set;
   its gate now asserts *owned* for the list and *immortal* for the four names
   that keep the sentinel (`__build_io_error`, `__fern_std{in,out,err}`).
4. **An `ownedPayloadMatches` arm frees the box shallow** right after its
   bindings are out (`emitOwnedPayloadArmBoxFree` → the map-get reclaim's
   `emitFreshBoxFreeSized`), at THAT arm's variant size — `Option[string]`'s
   `Some` and `None` differ under two-word strings. is_unique-gated, so an
   aliased box is only dec'd. The join reclaim is suppressed for such a match:
   both firing would free the box twice.
5. **`ownedCallResultType` admits the family** (`rcOwnedResultBuiltins`), which
   is what gives a `var o = r.read_line()` local, an argument temp
   `sink(env(k))` and a discarded `env(k);` the same release a user function's
   result gets — and, through the enum's deep drop, the payload with it.

## The trap this sets, and what does catch it

The classification and the allocation size are **one fact in two files**. A
helper in `rcResultOwned` whose arm still allocates below the uniform size is
not a leak; it is a freelist that hands a 16-byte block to a later 32-byte
request, and its eventual symptom is unrelated data corruption. Neither the
sanitizer nor the exit code sees it: with `env`'s `None` put back to 8 bytes
the corpus case still returns 0 and reports no over-release.

What does see it is `live_bytes` going **NEGATIVE** — the case reads
`leaks -12816 bytes` — because the free rounds up to a larger class than the
alloc did. That is the tell to recognise, and it is why the leak gate compares
against 0 rather than a floor. The wasm gate pins the classification against
`helperResultBoxCallers`; nothing reads the SIZE out of the emitted assembly,
so a new arm still relies on this indirect signal.

## The other shapes, checked

`if let Some(v) = env(k)`, `let Some(v) = env(k) else`, a match-EXPRESSION
scrutinee and `read_file(p)?` in one 100-round program, byte-identical output
and no over-release under `-sanitize`: x86-64 `801/101/16 000` →
`801/501/9 600`, arm64 `1002/102/19 200` → `1002/502/12 800`, wasm
`904/2/20 800` → `904/302/19 200`. The reclaim reaches all four without a
double free; what is left there is the try-op path, which is not this issue.

## What is left

- **The handle keeps its sentinel**, so #8398's `stdout()` / `open_*` residue
  stands. Making it counted needs `__fern_make_handle` to allocate through
  `__fern_alloc_rc1` (its block base is currently 8 bytes below what the IR's
  `__fern_box_free(handle, 4)` would hand back, so the release is only safe
  because the sentinel makes it inert) AND the receiver-ownership question
  #8398's third bullet names: `paramVerdictFacts.verdict` has no facts for a
  builtin callee, so it answers `Owned`, and `w.write(x)` therefore either
  `rc.inc`s the handle with no matching release or, at a last use, MOVES it
  into a helper that borrows. Measured with the handle counted anyway: the
  `alloc_flat_writer_accumulator` census row stays at 2 and only its byte count
  moves, so the handle change buys nothing until the receiver question is
  answered. Backed out for that reason.
- **`Result[void, IoError]`'s Ok box is 16 bytes in the natives' runtime and
  8 in the IR's layout** (`payloadLayout` puts the unit at +4; both natives
  store it at +8 and allocate for that). Now that the box is freed the two
  sizes have to agree: each `write_file` / `remove_file` / `create_dir_all` /
  `remove_dir_all` / `access` call returns a 32-byte block to the 16-byte
  class. Over-provisioned rather than overrun, so it wastes 16 B a call and
  reads as a leak; a 100-round probe is `300/200/6400` where the exact answer
  is `300/200/3200`. Which side is wrong is a layout decision, not a codegen
  one — filed as #8809, with the trap that the `+8` store must be settled
  before the allocation shrinks.
- **`__fern_writer_write` leaks its 12-byte wasm scratch buffer per call** —
  3 200 B over the 200-call probe, present before this change too. Same class
  as the per-call scratch #8399 fixed for `read_chunk`. Filed as #8808.
- **The self-host still returns immortal boxes.** #8402 gave it the payload
  half; this is the box half, and it is the goal-2 twin of this entry.

## Gates

`conformance/cases/alloc_flat_read_chunk` needed rewriting, and the reason is
worth keeping: its assertion **encoded the leak**. It compared the fresh bytes
of a 40-read round at 1024 against one at 16 and allowed a 2x factor "that
tolerates the per-call Result box (a constant, independent of the chunk)" —
and that constant was the denominator. With the boxes reclaimed the narrow
round falls from 1312 fresh bytes to 32 and the wide one from 2288 to 1040,
which is strictly better and fails the ratio. It now asserts what is actually
invariant: a SECOND round identical to the first must bump nothing at all, at
both sizes. That reads "constant" here and "scales" on the pre-#8405 compiler,
so the fixture is now a direct gate for this issue as well as #8396's, and its
reclaim-observable marker still holds.

`alloc_flat_writer_accumulator` moves 4 → 2 and `alloc_flat_read_chunk` 5 → 1
in `internal/e2e/testdata/conformance-leak-census.txt`; nine other I/O rows
move with them, two of them to 0. The rc corpus gains
`io_option_box_reclaimed_per_call`, absent from all three leak-baseline tables
and therefore asserted at zero — it covers the four consuming sites at once
and, because `env` of an unset name answers `None` every round, it is also the
case that pins the box SIZING.
