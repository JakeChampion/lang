# 2026-09-07 — a generic enum instantiation is deep-drop-wired (#8829)

`ownedByDefaultShapeIn` is gated on `typeDeepDropWired`, and that walk read a
generic enum's DECLARED payloads. Generic enums are not monomorphised: one
`EnumDecl` serves every instantiation and its payloads are `ParamType{T}`, with
the concrete types in `EnumType.Args`. No case in the walk matches a
`ParamType`, so it fell through to `false` — and every `Option[…]`, every
`Result[…]`, and every struct transitively holding one was outside the owned
model and lost every path built on it.

The drop EMITTER has substituted since #5917 (`emitEnumSlotDrop` ->
`substituteEnumDecl(ed, et.Args)`). So the capability predicates were answering
about a shape codegen never lowers.

## What it cost

`std/io_buffered`'s `BufWriter` carries `err: Option[IoError]`. That one field
took `buf: b.buf + s` off #8785's in-place field append and back onto a fresh
box plus a whole-buffer copy per write.

3M 17-byte writes through the real stdlib `BufWriter`, x86-64 `-O`, output to
`/dev/null`, 51 MB written and byte-identical either way:

| cap | before | after |
|---|---|---|
| 1 KiB | 118 ms | — |
| 4 KiB | 290 ms | — |
| 64 KiB | 3903 ms | 43 ms |

Scaling with the cap is the signature: the work per append is the buffer, so
the total is quadratic in how much is buffered between flushes.

## The bisection, because the field type is the whole finding

Same accumulator, same loop, one field changed. `cap: 65536`, 3M appends:

| third field | ms |
|---|---|
| `w: Writer` | 102 |
| `err: Flag` (a NON-generic enum) | 49 |
| `xs: i32[]` | 112 |
| `err: Option[i32]` | 5459 |
| `err: Option[string]` | 4561 |

A non-generic enum field is fine and `Option[i32]` — whose payload needs no
reclamation at all — is not. That is what pointed at the substitution rather
than at anything about enums or about drops.

## Why the Map exclusion still holds

`Option[Map[K, V]]` must stay out: `__map_drop_values` is not pulled into a
generated `__drop_enum_` body on wasm and Map key/value reclamation is an open
gap. It stays out for free, and only because the walk now substitutes — the
Map is in `Args` and is invisible in the shared decl, so the check that
excludes it is the same one that admits `Option[i32]`. Pinned both ways in
`internal/ir/rc_caps_test.go` and by
`TestOptionOfMapStaysOutOfTheOwnedModel`.

## Witnessed, not contract-only

The gate that matters is the BOX's runtime uniqueness, and admitting the type
into the owned model must not weaken it. `genericEnumFieldAliasSrc` holds a
second name for the box across the append on all three backends; the alias
reads the un-grown buffer and the counts balance. `FERN_LEAKCHECK` over a
struct carrying `Option[string]`, `Option[i32]`, `Option[i32[]]`,
`Option[Inner]`, `Option[Option[string]]` and `Result[string, i32]` is balanced
at 0 live bytes on x86-64, arm64 and wasm, with 4007 -> 141 allocations over
2000 appends.

arm64 takes the ownership decision but keeps the plain concat — it has no
`__fern_str_append` helper (`strAppendAvailable`), so its win is the box reuse
alone. That helper is the next lead for this shape.

## What the box-temp identity dec tells you

`TestBoxTempUnderPointerResultIsReleased` (#8755, #8763) went red, and reading
it is the clearest statement of what this change does. `boxTemp` is gated on
`!toOwnParam`, and `ownedByCalleeAt` is now true for that struct, so the
identity-guarded release is not emitted — the owned-by-default retain plus the
callee's exit dec is the pairing instead. Balanced on x86-64, arm64 and wasm
with `-sanitize` silent, and 3000 -> 2000 allocations over 1000 iterations.

The test's SUBJECT was the bug. Its struct carried an `Option[i32]` field
copied from BufWriter, and that field is the only thing that put it in the
mechanism's domain: the same program with a plain `n: i32` field emits no
guarded drop on unmodified main either. Repointed onto the addressTaken rung —
`put` reached through a function value as well as by name — which is a real
reason for a callee not to own its argument (OpCallIndirect has no callee name,
so no call-site retain) and reads identically before and after. The `Option`
field stays in the struct, where it must now change nothing.

## Two null results worth keeping

**The self-host compiler does not move.** `examples/self_host/fern.fern` built
by the fixed compiler is BYTE-IDENTICAL to the same file built before it, so
the 15.34M/12.15M pair in `.github/alloc-baseline.txt` is untouched by this and
whatever gap a fresh run shows against it is older drift. Checking the binary
was quicker than re-running the bench and is the stronger statement.

**The coreutils barely move**, and the reason is instructive: they already
route around this. `cat` builds a whole chunk into a local string and hands
`gnu.put` one large write, so `write_string`'s fitting branch never runs; the
comment in `format_chunk` about taking the output string "out of the state for
the loop" is that workaround written down. `block_bytes()` is 4096, which
bounds the quadratic to ~100 ns per write. Raising it to 64 KiB is now free —
it was a 13x loss before — but realising the win means putting the utilities
back on the natural idiom, which is its own change.
