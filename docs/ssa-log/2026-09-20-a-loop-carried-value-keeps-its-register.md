# 2026-09-20 — a loop-carried value keeps its register

After the immediates entry, `int_loop` on the self-host register path was
10 instructions per iteration against native's 4, and the listing named
the rest: `movq %r8, %r9; addq %r10, %r9; … movq %r9, %r8` — a value
computed beside its phi and copied back over it — twice, and an empty
block between the branch and the body.

## What changed

Three things, all in `ssa.fern`; the emitters change one line each.

**The allocator coalesces phi mates** (`regalloc_linear`, `phi_mates`,
`mate_interferes`). A phi's result takes the register of the operand that
arrives from before it when that operand dies at the phi; a loop-carried
operand — one defined at or after its phi — takes the phi's register when
the phi dies at its definition, or, when the phi lives on, when no use of
the phi is reachable from the operand's definition without passing the
header (where the phi is defined afresh). That is exactly the condition
under which the two are never live at once: `int_loop`'s `sum` is read
after the loop, but only through the header's exit, so `sum + i` may
compute into `sum`'s register; a loop whose body reads the old value
after the update, exits from the latch with both, swaps two carried
values, or reads the outer phi inside an inner loop while the update is
live, keeps two registers. The check is a reachability walk per pair and
runs only when the phi still holds its register. An interval that shares
a register and is later evicted takes its mates to the frame with it.

**A phi reads its operand on the edge.** The scan recorded a phi's
operands at the phi's own position inside the header, so an operand
arriving from before a loop looked like a value used inside it and the
loop-invariant extension held its register for the whole loop. It is
read at the predecessor's terminator, and its interval now ends there,
which is what lets the header's phi take it over: `movq $0, %rsi` is
`sum`'s home from the start and the entry edge moves nothing.

**The edges skip empty blocks** (`thread_forwarding`). `prune_dead` and
`prune_trivial_phis` leave blocks with no instructions and a plain branch
wherever an arm or an exit carried only dead values; every edge into such
a block now goes to its target when that target has no phis (a phi's
operands are per predecessor, so an edge into a phi block stays), and a
block nothing reaches afterwards is dropped, along with the slot every phi
of its successors held for it (a labelled `continue` out of an inner loop
that never exits leaves the outer header a predecessor nothing reaches; the
allocator once took that slot's operand, which nothing defined, for a
loop-carried value and indexed a block at -1). The header's branch lands on
the body directly and the body falls through from the header.

`int_loop`, x86-64: `cmpq $3000000, %rdi; jge exit; addq %rdi, %rsi;
addq $1, %rdi; jmp header` — 5 per iteration. Native's 4 rotates the
loop so the compare sits at the bottom.

## Measured

Every `examples/bench` program, x86-64, retired instructions under
callgrind, before (the immediates entry's "after") and with this change,
the native register backend for scale; the three map benchmarks left out
as before (#9608), `sort_strings` and `string_find_byte` reported in
#9850 and #9863.

| bench | before | after | Δ | after / native ssa |
|---|---|---|---|---|
| `int_loop` | 30,000,033 | 15,000,026 | **−50.0%** | 1.25x |
| `array_index` | 41,704,949 | 29,584,544 | −29.1% | 1.08x |
| `closure_call` | 198,036,086 | 164,352,083 | −17.0% | 0.87x |
| `call_overhead` | 30,526,191 | 25,376,134 | −16.9% | 1.90x |
| `sort_inplace` | 106,672,190 | 92,072,204 | −13.7% | 1.26x |
| `array_with` | 97,029,032 | 83,917,828 | −13.5% | 1.13x |
| `tokenize` | 152,930,093 | 136,190,089 | −10.9% | 1.52x |
| `sort_ints` | 87,871,786 | 80,365,796 | −8.5% | 1.28x |
| `string_slice` | 81,780,093 | 75,360,089 | −7.9% | 0.53x |
| `array_append` | 72,117,642 | 66,517,197 | −7.8% | 1.11x |
| `enum_match` | 174,150,084 | 162,675,080 | −6.6% | 1.73x |
| `record_update` | 405,048,758 | 388,644,367 | −4.0% | 2.26x |
| `string_scan` | 75,851,337 | 72,798,177 | −4.0% | 0.76x |
| `pvec_with` | 884,677,293 | 854,036,995 | −3.5% | 4.63x |
| `pmap_insert` | 731,195,411 | 706,672,175 | −3.4% | 3.68x |
| `utf8_ingest_validated` | 27,503,791 | 26,605,934 | −3.3% | 1.81x |
| `ordmap_insert` | 373,379,827 | 362,025,651 | −3.0% | 0.83x |
| `utf8_ingest_unchecked` | 22,483,191 | 21,989,534 | −2.2% | 2.00x |
| `struct_drop` | 542,180,204 | 534,020,196 | −1.5% | 0.88x |
| `ascii_scan` | 71,053,360 | 70,177,305 | −1.2% | 1.84x |
| `string_build` | 238,530,822 | 235,687,717 | −1.2% | 1.92x |
| `string_count_byte` | 270,900,993 | 270,870,955 | −0.0% | 2.00x |
| `string_rfind_byte` | 123,699,910 | 123,651,879 | −0.0% | 1.99x |

Nothing regressed and every exit status is unchanged. Seven of the
twenty-three now sit within 1.3x of the native register backend and five
below it; what keeps the rest above it is not in the emitter (the
association-list map under `pmap_insert` and `pvec_with`, #9608; the
per-call bookkeeping under `call_overhead` and `record_update`).

The whole compiler's emitted text, the same source compiled by the
compiler before and after: 2,739,512 to 2,573,342 instructions on x86-64
(−6.1%) and 2,718,847 to 2,546,604 on arm64 (−6.3%).

The gates: `TestSelfHostSSABackendAgreesWithStackMachine` on both ISAs
(arm64 under qemu) with two new programs — `carried_pairs`, every shape in
which sharing the register would be wrong, and `empty_blocks`, every
shape that leaves a forwarding block — `TestSelfHostSSALoopIsFiveInstructions`,
which reads the emitted text of a counted loop on both targets and finds
the compare, its branch, the two adds and the back edge and nothing else
between the header's label and the back edge, the SSA lifetime, semantic,
units, physical-RC, dependency, kind-registry, frame-sizing, dyn-dispatch,
loop-tail-label and admission-census tests, `TestSelfHostSemanticProduction`
whole, `TestRepoComplexityRatchet`, `TestSelfHostPerModuleEmitAllFixpointX86_64`,
`TestSelfHostStage2Compiler`, and `make bootstrap` with the pinned stage0.
