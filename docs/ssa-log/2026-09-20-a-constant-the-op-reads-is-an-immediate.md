# 2026-09-20 — a constant the op reads is an immediate

The entry before this one left `int_loop` at 12 instructions per iteration
on the self-host register path and named what remained: two of them were
`movq $3000000, %rax` and `movq $1, %rax`, constants materialised into a
register so that a compare and an add could read them from one. The native
register backend reads both as immediates.

## What changed

`ssa.imm_operands` marks the integer constants an emitter reads as
immediates. A constant qualifies when it lies in the ISA's range and every
use is a binary op reading it as the right operand of an op with an
immediate form, or as the left operand of one the emitter can turn round:
the commutative ops, and the comparisons, which flip
(`ircore.flip_int_cmp_kind`, which replaces the x86-64 emitter's private
copy). A use anywhere else keeps it in a register — a shift count, a
divisor, a call argument, a phi operand, a store — and so does an op whose
other operand is a constant too: the optimiser's folds do not run in the
production path, so `0 + 1` reaches the emitter, and swapping such a pair
would read a constant with no home as a spill slot. The allocator
(`regalloc_linear`, a `no_home` argument) gives a marked constant no
register and no slot; the emitter skips its definition and prints the
value at each use.

- **x86-64**: any i32-range constant, on the right of `add`, `sub`,
  `imul`, `and`, `or`, `xor` and the compares, or on the left of the
  commutative ones and the compares. `imul` with an immediate goes out in
  the three-operand form, `imulq $10, %rsi, %rsi`, which is the one form
  the in-process assembler encodes.
- **arm64**: 0 to 4,095, on the right of `add`, `sub` and the compares, or
  on the left of `add` and the compares. `mul` has no immediate form and
  the logical ops take bitmask immediates, which the 12-bit range does not
  describe, so those keep a register.

The fused compare a branch consumes reads its immediate the same way,
`cmpq $3000000, %r10` and `cmp x13, #3000`, flipping the condition when
the constant was on the left.

`int_loop` on x86-64 is now `cmpq $3000000, %r10; jge` and
`movq %r8, %r9; addq %r10, %r9; movq %r10, %rbx; addq $1, %rbx;
movq %r9, %r8; movq %rbx, %r10; jmp` — 10 per iteration against native's
4. What is left is the phi copies an allocator that coalesced the
loop-carried pair would not need (4) and the empty block the back edge
jumps through (1). On arm64 the bound 3,000,000 stays in the literal pool
(`ldr x15, =3000000`) and only the increment is an immediate.

## Measured

Every `examples/bench` program, x86-64, retired instructions under
callgrind, before (the previous entry's "after") and with this change, the
native register backend for scale. The three map benchmarks are left out
as before (#9608); `sort_strings` and `string_find_byte` moved by orders
of magnitude in #9850 and #9863 for reasons of their own and are not
this change's to report.

| bench | before | after | Δ | after / native ssa |
|---|---|---|---|---|
| `int_loop` | 36,000,035 | 30,000,033 | **−16.7%** | 2.50x |
| `call_overhead` | 34,826,289 | 30,526,191 | −12.3% | 2.28x |
| `utf8_ingest_validated` | 29,199,442 | 27,503,791 | −5.8% | 1.87x |
| `utf8_ingest_unchecked` | 23,617,042 | 22,483,191 | −4.8% | 2.04x |
| `array_index` | 43,745,151 | 41,704,949 | −4.7% | 1.52x |
| `sort_inplace` | 111,980,924 | 106,672,190 | −4.7% | 1.46x |
| `closure_call` | 207,648,087 | 198,036,086 | −4.6% | 1.05x |
| `tokenize` | 159,680,094 | 152,930,093 | −4.2% | 1.71x |
| `array_with` | 100,315,226 | 97,029,032 | −3.3% | 1.31x |
| `pvec_with` | 914,644,665 | 884,677,293 | −3.3% | 4.79x |
| `string_slice` | 84,480,094 | 81,780,093 | −3.2% | 0.58x |
| `pmap_insert` | 750,681,123 | 731,195,411 | −2.6% | 3.81x |
| `enum_match` | 178,425,085 | 174,150,084 | −2.4% | 1.85x |
| `sort_ints` | 90,031,678 | 87,871,786 | −2.4% | 1.40x |
| `array_append` | 73,717,763 | 72,117,642 | −2.2% | 1.20x |
| `string_scan` | 77,457,988 | 75,851,337 | −2.1% | 0.79x |
| `ascii_scan` | 72,113,895 | 71,053,360 | −1.5% | 1.86x |
| `ordmap_insert` | 378,843,853 | 373,379,827 | −1.4% | 0.86x |
| `struct_drop` | 549,140,205 | 542,180,204 | −1.3% | 0.89x |
| `string_build` | 241,457,623 | 238,530,822 | −1.2% | 1.95x |
| `record_update` | 408,334,952 | 405,048,758 | −0.8% | 2.35x |
| `string_count_byte` | 270,913,015 | 270,900,993 | −0.0% | 2.00x |
| `string_rfind_byte` | 123,729,932 | 123,699,910 | −0.0% | 1.99x |

Nothing regressed and every exit status is unchanged. The whole compiler's
emitted text, the same source compiled by the compiler before and after:
2,771,958 to 2,739,512 instructions on x86-64 (−1.2%) and 2,750,070 to
2,718,847 on arm64 (−1.1%). A modest move: most constants in the compiler
are call arguments, array indices and stores, none of which an ALU op
reads.

The gates: `TestSelfHostSSABackendAgreesWithStackMachine` on both ISAs
(arm64 under qemu) with a new `immediates` program — constants on either
side of every op that takes one, negative, at i32 wrap, in a fused branch,
above the arm64 range and above imm32, and one a division reads —
`TestSelfHostSSAConstantsAreImmediates`, which reads the emitted text of a
counted loop on both targets and finds the add and the compare on
immediates and the divisor still in a register, the SSA lifetime,
semantic, units, physical-RC, dependency and kind-registry tests,
`TestSelfHostSSAFrameIsSizedBySpills`, `TestSelfHostSemanticProduction`
whole, and `make bootstrap` with the pinned stage0.

## Trap

The first cut admitted a constant whenever its own uses qualified, and
`n = n + 1` with `n` still at its initial `0` is `add v, c0, c1`: both
operands constants, both admitted, the swap put `1` on the left, and the
emitter read the register-less `1` through `ssa_load` as spill slot −1.
arm64 `host_calls` counted 58 for 63, x86-64 hid the same hole behind the
`imulq` encoding error. The rule is per op, not per constant: an op whose
other operand is a constant keeps this one in a register.
