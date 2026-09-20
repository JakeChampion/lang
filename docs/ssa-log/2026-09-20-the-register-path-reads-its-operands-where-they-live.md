# 2026-09-20 — the register path reads its operands where they live

#9640 and #9669 measure the native register backend against the native stack
machine and find single instructions to save per loop iteration. The
self-host's register path, which is the default on both native ISAs and
whose output wins ties, had not been put on the same scale. Retired
instructions under callgrind, `examples/bench/int_loop` (3,000,000
iterations of `sum = sum + i; i = i + 1`), x86-64:

| | Ir | per iteration |
|---|---|---|
| native stack machine | 24,000,017 | 8 |
| native register backend | 12,000,023 | 4 |
| self-host stack machine | 78,000,056 | 26 |
| self-host register path | 72,000,045 | **24** |

The self-host's register path was six times the native one on the codegen
floor, and its own stack machine was little worse. The loop body showed
why: every binary op loaded its right operand into `%rcx`, its left into
`%r11`, computed there and stored to the home (four instructions for one
`add`), the fused compare did the same, and the two loop-carried phis were
staged through two frame slots, eight instructions per back edge for two
copies.

## What changed

Three parts of `asm_ir.fern`'s and `asm_arm64_ir.fern`'s register emitter,
the same on both:

- **A binary op computes into the result's home.** For add, subtract,
  multiply, and, or, xor and the integer compares, `ssa_bin_in_place`
  reads each operand from its home — only a spilled operand passes through
  a scratch — and writes the result's home directly. On x86-64 the
  two-operand form moves the left operand into the destination first, so
  a right operand that lives in the destination would be overwritten: the
  operands swap for a commutative op, a directional comparison flips
  (`ssa_flip_cmp`), and a subtraction in that shape computes in the scratch
  as before. arm64's three-operand forms need none of that. Division, the
  shifts and the arms shared with the stack machine keep the scratch path.
- **A fused compare reads its homes.** `cmpq b, a` on the operands where
  they are, not after two moves into the scratch pair.
- **The phis of an edge are parallel moves.** `ssa_parallel_moves` emits a
  move whose destination no pending move still reads, one instruction
  between two homes or two through the scratch between two slots; when
  every pending destination is still read, the moves form a cycle, one
  source location is parked in the scratch and every move reading it takes
  it from there. A phi whose operand already sits in its home moves
  nothing. The frame's phi stage area (`nstage`, `ssa_max_phis`) goes with
  it.

`int_loop` is now `movq $3000000, %rax; cmpq %rax, %r10; jge` and
`movq %r8, %r9; addq %r10, %r9; movq $1, %rax; movq %r10, %rbx; addq %rax,
%rbx; movq %r9, %r8; movq %rbx, %r10; jmp` — 12 per iteration. What is left
is named in the same listing: constants materialised into a register where
an immediate would do (2), the phi copies an allocator that coalesced the
loop-carried pair would not need (2 plus the 2 compute-in copies), and the
empty block the back edge jumps through (1).

## Measured

Every `examples/bench` program, x86-64, retired instructions, self-host
register path before and after, native for scale (the three map
benchmarks are left out: the self-host's map is an association list,
#9608, and dominates them by 1,000x whatever the emitter does):

| bench | before | after | Δ | after / native ssa |
|---|---|---|---|---|
| `int_loop` | 72,000,045 | 36,000,035 | **−50.0%** | 3.00x |
| `string_find_byte` | 15,139,906,101 | 9,241,419,929 | −39.0% | 135.71x |
| `array_index` | 69,926,868 | 43,745,151 | −37.4% | 1.60x |
| `call_overhead` | 49,576,543 | 34,826,289 | −29.8% | 2.60x |
| `closure_call` | 267,810,095 | 207,648,087 | −22.5% | 1.10x |
| `utf8_ingest_validated` | 37,007,822 | 29,199,442 | −21.1% | 2.00x |
| `utf8_ingest_unchecked` | 29,744,022 | 23,617,042 | −20.6% | 2.16x |
| `sort_ints` | 111,102,106 | 90,031,678 | −19.0% | 1.44x |
| `sort_inplace` | 136,381,325 | 111,980,924 | −17.9% | 1.53x |
| `sort_strings` | 44,006,297,217 | 36,926,188,529 | −16.1% | 213.70x |
| `array_with` | 118,382,909 | 100,315,226 | −15.3% | 1.36x |
| `tokenize` | 185,300,103 | 159,680,094 | −13.8% | 1.78x |
| `pvec_with` | 1,059,437,993 | 914,644,665 | −13.7% | 4.96x |
| `string_slice` | 96,540,103 | 84,480,094 | −12.5% | 0.60x |
| `pmap_insert` | 851,535,116 | 750,681,123 | −11.8% | 3.91x |
| `enum_match` | 198,225,094 | 178,425,085 | −10.0% | 1.89x |
| `array_append` | 81,718,532 | 73,717,763 | −9.8% | 1.23x |
| `string_scan` | 85,401,973 | 77,457,988 | −9.3% | 0.81x |
| `ordmap_insert` | 407,016,765 | 378,843,853 | −6.9% | 0.87x |
| `struct_drop` | 588,140,214 | 549,140,205 | −6.6% | 0.90x |
| `string_build` | 255,846,832 | 241,457,623 | −5.6% | 1.97x |
| `record_update` | 429,679,435 | 408,334,952 | −5.0% | 2.37x |
| `ascii_scan` | 75,400,665 | 72,113,895 | −4.4% | 1.89x |
| `string_rfind_byte` | 123,832,104 | 123,729,932 | −0.1% | 1.99x |
| `string_count_byte` | 270,979,181 | 270,913,015 | −0.0% | 2.00x |

Nothing regressed, every exit status is unchanged, and the median move is
around −12%. The whole compiler's emitted text falls with it: 3,016,846 to
2,770,652 instructions on x86-64 (−8.2%) and 3,022,863 to 2,748,555 on
arm64 (−9.1%), the same source compiled by the compiler before and after.

The gates: `TestSelfHostSSABackendAgreesWithStackMachine` on both ISAs
(arm64 under qemu), the SSA lifetime, semantic, units, physical-RC and
dependency IR tests on both, `TestSelfHostSSAFrameIsSizedBySpills`,
`TestSelfHostSemanticProduction` whole, `TestSelfHostStage2Compiler` and
`TestSelfHostPerModuleEmitAllFixpointX86_64`; and the conformance corpus
compiled both ways and run — 522 cases agree on x86-64, 520 on arm64, none
diverge.

## Two things found on the way

**The agreement test was comparing the register path with itself.** Its
reference side compiled with no `-backend`, which has been the register
path since the flip; it now asks for `flat` by name. It caught nothing
until this change because the two sides were one output.

**Two more self-host gaps of the map's size, neither the emitter's.**
`sort_strings` runs 214x the native register backend because the typed
lowering reads an element of `__cmp_insertion`'s array into a local that
outlives the inner loop's `.with`; a projection borrows its array, so the
receiver is retained rather than moved and that `.with` copies the whole
array once per outer iteration (#9849, fixed by the commit after this
one; the AST lowering and native are level). `string_find_byte` runs 136x because both self-host lowerings
intercept `string.index_of` and emit the stack IR's byte loop, where
native reaches std/string's method and `__memchr` for a one-byte needle
(#9848).

## Trap

The parked location of a parallel move is a home code, and a spill slot's
code is negative: `-2` is slot 0. A sentinel chosen from that space
(`0 - 2` for "nothing parked") made every move out of slot 0 read the
scratch instead, and the loop that carried its induction variable through
slot 0 ran forever. The flag for "a location is parked" is a boolean of
its own.
