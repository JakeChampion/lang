# Measured 2026-09-16: every small copy paid a `rep movsb` startup

With the allocator, the rc primitives and the divisions out of the way,
`struct_drop` still took 1.6x the flat build's time and `to_string` 1.8x,
and the instruction counts were within 15% with no cache or branch cost
to account for the rest (callgrind: 528M instructions to the flat 536M
on `struct_drop`, 24 data-cache misses each). The cost per instruction
was in one helper: every string and array copy on x86-64 SSA went through
`__ssa_bcopy`, a bare `rep movsb`, and every zero fill through a bare
`rep stosb`. A rep string instruction costs tens of cycles to start on the
Haswell-class baseline whatever the length, and nearly every copy the
backend makes is a few bytes. Below 64 bytes both are loops now (8-byte
moves with an overlapping tail, bytes under 8); rep takes over from 64.
Best of five, x86-64 native, main at bfcb94d (before #9432) on both sides:

| bench | flat | SSA before | SSA after | ratio before → after |
| --- | --- | --- | --- | --- |
| `struct_drop` | 0.065 s | 0.105 s | 0.064 s | 1.62x → **0.98x** |
| `string_build` | 0.017 s | 0.023 s | 0.016 s | 1.35x → **0.94x** |
| `to_string` loop | 0.026 s | 0.048 s | 0.028 s | 1.85x → 1.08x |
| struct build loop | 0.036 s | 0.052 s | 0.034 s | 1.44x → **0.94x** |
| `tokenize` | 0.014 s | 0.014 s | 0.013 s | 1.00x → 0.93x |
| `map_int`, `ordmap_insert`, `array_append` | | | | unchanged |

The last two loops are the `to_string` and the `mk` halves of `struct_drop`
on their own. `docs/PERFORMANCE-AUDIT-2026-08.md` had measured the same
trap on the flat backend's copy a month earlier.

### Measured 2026-09-16: the SSA paths never ran the IR battery

Both SSA build paths lowered with `ir.LowerWith` and then called
`ir.ElideClosurePair` alone, where the flat backends call
`ir.OptimizeProgram` before their emitters: tail-call optimisation,
`Inline` twice around `Defunctionalise` + `ElideClosurePair` +
`InlineZeroCaptureClosures`, then the per-function tail. So every SSA build
compiled the un-inlined, un-defunctionalised program — `core/map` passing
its hash and eq functions as values into `__map_lookup_keyed`, an indirect
call per probe that the flat backends had turned into direct, inlined code
all along. That was `map_int`'s 1.6x instruction count against the flat
build, and much of "codegen quality in loop bodies". The SSA paths now run
the same battery. Best of five, ratio to the flat build; x86-64 native,
arm64 under qemu on main before #9433's inlining:

| bench | x86-64 before | x86-64 after | arm64 before | arm64 after |
| --- | --- | --- | --- | --- |
| `map_int` | 1.22x | **0.89x** | 1.81x | **0.90x** |
| `map_probe_chain` | 1.17x | **0.89x** | 1.76x | **0.86x** |
| `map_string` | 1.00x | 0.88x | 0.77x | **0.49x** |
| `call_overhead` | 1.00x | **0.50x** | 1.35x | **0.76x** |
| `tokenize` | 1.00x | 0.86x | 0.80x | 0.60x |
| `ordmap_insert` | 0.91x | 0.82x | 2.49x | 2.02x |
| `pvec_with` | | | 2.44x | 1.48x |

Outputs identical on every row. Static instructions move both ways: the
inlined callees are culled (`ordmap_insert` 6,493 → 3,048, `coreutils/sort.fern`
62,249 → 58,962) where the map programs grow 3-4% with the inlined bodies.
`coreutils/sort.fern` on 2M lines: `sort` 2.73 s → 2.68 s, `sort -n`
6.25 s → 6.16 s.

The battery's output is IR the SSA layer had never seen, and the corpus
differential caught four latent bugs on its first run with it, on both
targets alike (316 agree, 8 refused, 4 diverge, against 328/0/0 before):

- `CmpFlip` rewrote `not (a > b)` into `a <= b` without the compared
  width, so the next SCCP round folded an i64 compare at 32 bits and the
  inlined `log2_floor` loop was never entered.
- SCCP rewrote a phi it proved constant without the constant's width,
  so an inlined i64 argument was materialised from its low 32 bits and
  `bit_length` ran its loop 64 times on every input.
- The lifter skipped the ops on a dead path but not the scopes they
  opened, so inlined drop glue after a `return_pair` reached its `else`
  with no `if` on the scope stack (`utf8__codepoint_at`,
  `http__http_parse_request`).
- `internal/ir`'s DCE did not count `return_pair` as a terminator, which
  is why that dead glue survived to the lifter at all.

Each has a unit test now; the differential is back to 328 agree, 0
refused, 0 diverge on x86-64 and 326 / 2 / 0 on arm64.
