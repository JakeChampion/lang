# x86-64 takes the call result too, and idiv is the reason it took longer

The slice `2026-09-17-the-x86-call-result-needs-one-more-thing.md` specified,
carried out. The recipe from arm64 transferred; so did the trap it warned
about, and the `idiv` constraint turned out to bite in a second place the
specification did not predict.

## The numbers

Whole compiler, `-emit asm`, against the same main:

| | flat | ssa before | ssa after | after, over flat |
|---|---|---|---|---|
| x86-64 | 3,048,987 | 3,996,154 | 3,795,946 | +24.5% |
| arm64 | 4,051,791 | 3,913,741 | 3,913,741 | -3.4% |

200,208 instructions, 5.0% of the x86-64 build. The gap to the stack
machine falls from 31.3% to 24.5%. arm64 is untouched.

Less than the 275,973 moves the measurement counted, because `%rax` is one
register among six: it also goes to ordinary values when nothing else is
free, and a value whose interval spans a division may not have it at all.

## What it took

As specified: the arms that are not call kinds moved off `%rax` to `%r11`,
which left the pool to make room; `ir_bin_asm` and the four float cores
(`ir_fround_rax`, `ir_f2i_sat_rax`, `ir_f2u_sat_rax`, `ir_i2f_rax`, all
reached from the unary arm) take their registers as parameters, so the
stack machine keeps `%rax` and the register path passes its own; `%rax`
became pool index 0; and `regalloc_linear` prefers it for a value a call
defines.

`idiv` is the exception the specification called out: it is hardwired to
`%rdx:%rax`, so the four guarded divisions still work there and the
allocator refuses `%rax` to any value whose interval spans one. That is a
second position list beside `calls`, built the same way and read by the
same `spans_call`.

## The two traps

**The one arm64 warned about.** A call arm forgets the scratch tracker on
entry, and while the working register and the ABI result register were the
same the closing store re-asserted it. Once they differ the tracker
outlives the `call` that clobbers the working register. Every call arm
forgets after the call as well. Reading the arm64 entry first meant this
cost nothing here.

**The one it did not.** The binary arm loads its left operand into the
register it computes in. For a division that register is `%rax` — and
`%rax` is now a home, so loading the left operand there destroyed a right
operand that lived in it. The right operand goes first now; `%rcx` is never
a home, so the reverse cannot happen. arm64 never had this because its
binary arm computes in a register that is not in the pool at all.

16 of 522 conformance programs caught it, and none of them under
`FERN_SSA_ONLY` on a single function — the same blindness the arm64 entry
records, for the same reason.

## Checks

- The backend gate on arm64-darwin.
- 522 conformance programs built for x86-64-linux on both backends and run
  under qemu, and 520 for arm64-linux run natively: same exit and same
  output on every one.
- Stage two identical on both targets.
- The complexity ratchet, the source lint and the testname gate.
