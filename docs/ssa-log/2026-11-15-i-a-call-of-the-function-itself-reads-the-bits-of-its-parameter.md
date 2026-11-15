# A call of the function itself reads the bits of its parameter

2026-11-15. All targets that use the register form. Follows
`2026-11-15-h-a-constant-field-costs-less-than-a-mask.md`.

## The shape

`safe` of nqueens walks a list and calls itself with `diagonal + 1`:

```
add $0x1,%rsi
movslq %esi,%rsi
mov %r8,%rdi
jmp ...
```

The sign extension is the wrap of the `i32` sum. Each use of `diagonal` in
the function reads only its low 32 bits: two sums that a compare of 32 bits
reads, and the next step. `drop_low_wraps` removes a wrap when no use reads
more bits than the wrap keeps. It counted each argument of a call as a use
of all 64 bits, so the wrap stayed.

## The change

A call of the function itself gives its argument to a parameter of the same
function. The argument needs only the bits that the uses of that parameter
read.

- A parameter now has a demand, as a phi has (`params`).
- `drop_low_wraps` does not give the arguments of such a call a fixed demand
  of 64 bits (`self_call`).
- After the demand spreads, each argument gets the largest demand of a
  parameter that it goes to (`self_call_demand`). The demand then spreads
  again from those arguments. This repeats until no demand changes.

A caller from outside gives a value with all of its bits, so its value is
correct for each use.

## Measured

Cachegrind instructions, x86-64, nqueens 10: 23,862,210 before and
23,082,978 after (-3.3%). rbtree, rbtree-ck, deriv and cfold give the same
binaries.

The compiler built with the change builds itself again byte for byte.

## Tests

`ssa_register_form_run` has three new checks:

- A call of the function itself reads the low byte of its argument, so the
  wrap goes.
- A call of another function reads each bit of its argument, so the wrap
  stays.
- A parameter that the function returns needs each bit, so the wrap of the
  argument stays.
