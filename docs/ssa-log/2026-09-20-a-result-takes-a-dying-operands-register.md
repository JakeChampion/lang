# 2026-09-20 — a result takes a dying operand's register

After the coalescing entry, `call_overhead`'s `step` — `acc + i * 3 - 1` —
was eight instructions on x86-64: each two-operand op moved its left
operand into a fresh register first, and the value the function returned
was copied once more into `%rax`. Its caller sat at 1.90x native's register
backend, and the byte loop of `__fern_string_from_bytes` at 12 per byte.

## What changed

Three things, all small.

**A result takes the register of an operand that dies at it.** The phi-mate
placement in `regalloc_linear` is now the general case: a value defined by a
phi, a two-operand integer op or a unary tries, in order, its phi mate and
each operand of its definition, and takes that value's register when it is
free or when its holder dies at this very definition — both emitters read
an operand before they write the result, or swap when the right one lives
in the destination. `acc + t` computes into `acc`'s register, `i * 3` into
`i`'s, and the loop counter's `i + 1` followed by the i32 wrap runs in
place: `step` is `imulq $3, %rdi, %rdi; addq %rdi, %rsi; subq $1, %rsi;
movq %rsi, %rax`.

**A branch falls through into a next-block false target.** The lift spells
some `while` conditions as `brif !(i < n) → exit else body`, and the
emitter always jumped to the false target on the inverted condition, then
jumped again to the true one — `jl body; jmp exit` with `body` the very
next block. When the false target is the next block and neither edge
carries phi moves, the branch is on the condition itself to the true
target and the false edge falls through (both ISAs).

**A loop-carried operand lives to its back edge, not to the end of the
function.** `extend_backedge_phi_intervals` stretched every loop-carried
phi operand to the last position of the function, so one that fed a phi in
a loop followed by a call was made to span that call and pushed into a
callee-saved register, which its phi — in a caller-saved one — could not
then share. The phi's read on the back edge, recorded at the latch's
terminator since the coalescing entry, is exactly the interval the
extension was standing in for, so the extension is gone.

## Measured

Every `examples/bench` program, x86-64, retired instructions under
callgrind, before (the AVX2 kernel entry's "after") and with this change,
the native register backend for scale; the three map benchmarks left out
as before (#9608), `sort_strings` reported in #9850.

| bench | before | after | Δ | after / native ssa |
|---|---|---|---|---|
| `array_index` | 29,584,544 | 23,544,242 | **−20.4%** | 0.86x |
| `closure_call` | 164,352,083 | 135,534,082 | −17.5% | 0.72x |
| `utf8_ingest_unchecked` | 21,989,534 | 19,030,789 | −13.5% | 1.73x |
| `call_overhead` | 25,376,134 | 22,001,011 | −13.3% | 1.64x |
| `utf8_ingest_validated` | 26,451,734 | 23,070,189 | −12.8% | 1.57x |
| `sort_ints` | 80,365,796 | 75,328,561 | −6.3% | 1.20x |
| `array_with` | 83,917,828 | 78,968,257 | −5.9% | 1.07x |
| `string_slice` | 75,360,089 | 70,920,088 | −5.9% | 0.50x |
| `tokenize` | 136,190,089 | 128,700,088 | −5.5% | 1.44x |
| `pvec_with` | 854,036,995 | 807,827,988 | −5.4% | 4.38x |
| `pmap_insert` | 706,672,175 | 676,688,083 | −4.2% | 3.52x |
| `sort_inplace` | 92,072,204 | 88,989,651 | −3.3% | 1.22x |
| `struct_drop` | 534,020,196 | 519,740,195 | −2.7% | 0.85x |
| `string_build` | 235,687,717 | 229,880,016 | −2.5% | 1.87x |
| `array_append` | 66,517,197 | 64,916,996 | −2.4% | 1.08x |
| `enum_match` | 162,675,080 | 159,075,079 | −2.2% | 1.69x |
| `ascii_scan` | 39,559,305 | 38,785,218 | −2.0% | 1.01x |
| `ordmap_insert` | 362,025,651 | 355,361,981 | −1.8% | 0.82x |
| `record_update` | 388,644,367 | 382,072,781 | −1.7% | 2.22x |
| `string_scan` | 72,798,177 | 72,509,524 | −0.4% | 0.76x |
| `int_loop` | 15,000,026 | 15,000,025 | −0.0% | 1.25x |
| `string_count_byte` | 135,774,955 | 135,744,905 | −0.0% | 1.00x |
| `string_find_byte` | 68,403,876 | 68,373,819 | −0.0% | 1.00x |
| `string_rfind_byte` | 68,349,879 | 68,319,822 | −0.0% | 1.10x |

Nothing regressed and every exit status agrees with native's. Twelve of the
twenty-four now sit within 1.25x of the native register backend and seven
below it. The whole compiler's emitted text, the same source compiled by
the compiler before and after, falls from 2,573,342 lines to 2,499,899 on
x86-64 (−2.9%) and from 2,546,604 to 2,495,540 on arm64 (−2.0%).

The gates: `TestSelfHostSSABackendAgreesWithStackMachine` on both ISAs,
`TestSelfHostSSALoopIsFiveInstructions`, `TestSelfHostSSAConstantsAreImmediates`,
the SSA lifetime, semantic, units, physical-RC, dependency, kind-registry,
frame-sizing, dyn-dispatch, loop-tail-label and admission-census tests,
`TestSelfHostSemanticProduction` whole, `TestRepoComplexityRatchet`,
`TestSelfHostPerModuleEmitAllFixpointX86_64`, `TestSelfHostStage2Compiler`,
and `make bootstrap` with the pinned stage0.
