# 2026-10-03 — a returned value asks for the result register

`ssa.regalloc_linear` and `ssa.return_prefs`, both native targets. Refs
#8171.

## The shape

A function returns its value in `%rax` (`x0` on arm64). The allocator took
no notice of that, so a returned value went to whatever register was free
first, and the return moved it across:

```
    imulq $3, %rsi, %rsi
    movq %rsi, %rax
    ret
```

The stage-2 binary held 11,995 such moves directly ahead of a `ret`, and
they ran 97 M instructions on the compile of `checker.fern`.

## What changed

A value a block returns, when it asks for no register already, now asks
for the register a call's result arrives in. A phi passes its ask to the
operands it merges that ask for nothing, last block first, so both arms of
a returned `if` compute into the result register. The ask is the same
`pref` that parameters and call arguments use: it is taken when the
register is free and the value does not live across a call.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at d46426a0 and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 23.910 G | 23.901 G (−0.04%) |
| moves into `%rax` right before a `ret`, stage-2 binary | 11,995 | 8,779 |

The gain is small because the hot cases stay. In `NameIndex.chain`, for
example, the result is loaded while the call result it indexes with still
holds `%rax`. An array read is not one of the instructions whose result
may take a dying operand's register, so the load lands in `%r8` and moves.
`std/crypto`'s five digests over 4 MiB: unchanged (1.297 G).

Emitted bytes change on 823 of the 1,968 `selfhost-emit-hashes` rows: 414
x86-64 and 409 arm64.

## Witnessed

`TestSelfHostReturnValueInResultRegister` is new. A function returning a
merge of two arms, and one returning the sign extension of a division's
remainder, must not move the result into `%rax` before the `ret`, and the
program must exit 42 on every host target. Also run: the opt-shapes,
spilled-mate, spilled-operand, leaf-frame, lea, merge-hint, phi, regalloc,
physical-rc, const-form, alloc-reuse and arr-inc-elems suites in
`internal/e2eselfhost`.
