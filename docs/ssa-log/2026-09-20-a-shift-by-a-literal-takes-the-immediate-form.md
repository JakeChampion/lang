# 2026-09-20 — a shift by a literal takes the immediate form

Found while reading `__fern_arr_slice`'s listing for #9875. The immediates
entry gave the binary ops immediate operands and left the shifts on the
scratch path beside division and remainder, because a variable count has to
reach `%cl` (x86-64) or be masked in place (arm64). A literal count needs
neither.

`(n * 8) + (n >> 3) + (k << 2)`, x86-64, before:

```
    movq $3, %r9
    movq %r9, %rcx
    movq %rsi, %r11
    andl $31, %ecx
    sarq %cl, %r11
    movq %r11, %r10
```

after:

```
    movq %rsi, %r9
    sarq $3, %r9
```

## What changed

Both register-path emitters route a shift whose count the allocator marked as
an immediate through the in-place emitter, which computes into the
destination. The mask folds into the literal at the operand's width, exactly
as the stack machine's `ir_shift_imm_asm` has done since the strength
reduction landed; that function now shares its text with the register path
rather than keeping a second copy hardwired to one register. A shift never
swaps its operands, so the commutative swap is skipped for it.

## Measured

x86-64, retired instructions under callgrind, against the rc-primitives entry
immediately before it. This is a small change and the numbers say so.

| bench | before | after | Δ |
|---|---|---|---|
| `utf8_ingest_validated` | 22,167,189 | 21,903,189 | −1.2% |
| `pvec_with` | 739,051,050 | 737,874,440 | −0.2% |
| `array_index` | 23,384,242 | 23,384,242 | 0.0% |
| `sort_ints` | 68,974,953 | 68,974,953 | 0.0% |
| `string_scan` | 72,302,697 | 72,302,697 | 0.0% |
| `tokenize` | 128,700,088 | 128,700,088 | 0.0% |

The whole compiler's emitted text falls 1,197 lines on x86-64 and 1,200 on
arm64, about 0.035%, which is roughly 300 shift sites at four instructions
each. Nothing regressed and every exit status is unchanged.

So the corpus barely moves: a shift by a literal is not on these programs'
hot paths. It is landed for two reasons rather than for the table. The
sequence it replaces was six instructions for one, which is the kind of thing
that should not survive being seen. And `__fern_arr_slice`'s two byte-count
multiplies are each one of these, inside the helper #9875 is about, so that
slice measures what it means once this is in.

`TestSelfHostSSAConstantsAreImmediates` gains the shift: the immediate-count
form is present on both ISAs, and neither the count register nor the mask a
variable count needs appears.
