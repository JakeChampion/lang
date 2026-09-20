# 2026-09-20 — the self-host's x86-64 byte kernels run native's 32-byte AVX2 loops

After the coalescing entry, `string_count_byte`, `string_find_byte`,
`string_rfind_byte` and `ascii_scan` sat at 2.0x the native register
backend on x86-64 for one reason: the four byte-scan kernels the self-host
emits inline (memchr, rmemchr, count_byte, ascii_run) ran 16 bytes an
iteration with SSE2, where native's run 32 with AVX2 first. AVX2 is inside
the x86-64-v3 baseline (`../BACKEND-PARITY.md`), so nothing guards it.

## What changed

`x86_native.fern` encodes the five VEX forms native's `avx.go` has and no
more — `vmovdqu` (load), `vpbroadcastb`, `vpcmpeqb`, `vpmovmskb`,
`vzeroupper` — with the 2-byte prefix wherever GNU as uses it. Each kernel
gets native's tiers: 32 bytes an iteration while a whole block remains,
then the SSE2 loop, then the scalar tail, with the upper halves cleared on
every way out of the AVX loop.

## Measured

x86-64, retired instructions under callgrind, before (the coalescing
entry's "after") and with this change, the native register backend for
scale. Only the programs that reach a kernel move; the other fifteen emit
no kernel and are unchanged to the instruction.

| bench | before | after | Δ | after / native ssa |
|---|---|---|---|---|
| `string_count_byte` | 270,870,955 | 135,774,955 | **−49.9%** | 1.00x |
| `string_find_byte` | 136,001,916 | 68,403,876 | −49.7% | 1.00x |
| `string_rfind_byte` | 123,651,879 | 68,349,879 | −44.7% | 1.10x |
| `ascii_scan` | 70,177,305 | 39,559,305 | −43.6% | 1.03x |
| `utf8_ingest_validated` | 26,605,934 | 26,451,734 | −0.6% | 1.80x |

Every exit status agrees with native's. `string_rfind_byte`'s remaining
1.10x is the reverse scan's entry, which aligns to the block end through a
scalar prologue native does not pay; `utf8_ingest_*` reach `ascii_run`
only for their ASCII prefix, and their gap is the decode loop, taken up in
the next entry.

## Checked

The kernel sweeps (`TestSelfHost{CountByte,Memchr,Rmemchr,AsciiRun}IR` on
x86-64, arm64 and wasm) run every length from 0 to 72, so both vector
tiers and every hand-over between them are crossed on every backend.
`TestSelfHostX86FormsMatchNative` pins the thirteen register mixes of the
five forms against native's encoder, `TestSelfHostX86GasVexGroundTruth`
against GNU as, and `TestSelfHostX86ByteKernelsRunAVX2` reads the tiers
out of the emitted x86-64: three broadcasts, three 32-byte compares, four
mask extractions and seven `vzeroupper` exits, one per way out of each
AVX loop. A first count expected eight exits; `count_byte` has one, since
it runs to the end of its block count rather than leaving on a hit.
