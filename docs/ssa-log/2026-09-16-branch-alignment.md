# Measured 2026-09-16: a jump on a 32-byte line ran the loop at half speed

The full bench sweep with every pending slice applied put `string_count_byte`
at 0.045 s on x86-64 SSA against 0.020 s for the same program built from
main, with the kernel's code and instruction count identical between the
two and only its address different. In the slow build the vector loop's
back-edge `jmp` sat on bytes 31 and 0 of a 32-byte line. That is the shape
the Skylake-family JCC erratum's microcode fix keeps out of the decoded-
instruction cache, so the ten-instruction loop ran from the legacy decoder
at half speed; whether a build hit it depended on where the helper before
it happened to end. The in-process assembler now pads before every jump,
call and return that would cross or end on a 32-byte line of the address
space (`relax.go`'s `branchPad`, on for the programs the driver links and
off for the GNU as byte oracle), for both x86-64 backends. Best of five,
x86-64 native, every pending slice applied:

| bench | flat before | flat after | SSA before | SSA after |
| --- | --- | --- | --- | --- |
| `string_count_byte` | 0.012 s | 0.010 s | 0.045 s | 0.023 s |
| `string_rfind_byte` | 0.008 s | 0.005 s | 0.016 s | 0.009 s |
| `string_scan` | 0.012 s | 0.011 s | 0.019 s | 0.019 s |
| everything else | | ±1 ms | | ±1 ms |

The remaining `string_count_byte` gap is the kernel's width: the flat
backend's scans 32 bytes an iteration with AVX2 where the SSA helper scans
16 with SSE.

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
