# 2026-09-28 — a value-block struct local is reclaimed (#10610)

AST lowering (`FERN_SEM_IR=`) only. The semantic lowering already balanced
every row here.

```fern
let h: Holder = { Holder { x: Inst { name: "w" + "", depth: n } } };
```

## Cause

The parser lowers a value block to an IIFE: a call to an `ORIGIN_BLOCK` lambda
whose body is `return tail`. `collect_fresh_in_stmt`, which finds the struct
locals a function reclaims at exit, tested the initialiser with
`is_fresh_struct_init`. That test saw a call to a lambda, not a struct literal.
`h` was therefore never registered, and it leaked with everything it held.

## Change

`block_tail_value` unwraps a value block that is nothing but its tail
(no arguments, a single `return`) and hands the tail to `is_fresh_struct_init`.
A block that declares a local before its tail is not unwrapped. The value
block's credit view drops the tail, so a block local stored into the struct
would read as unescaped and be freed under the holder.

## Measured (`FERN_LEAKCHECK=1`, AST lowering, allocs / frees)

| row | before | after |
|---|---|---|
| `block_tail` (the issue's shape) | 20 / 0 | 20 / 20 |
| `block_tail_lent` (`h` lent to a borrowing call) | leaks | balanced |
| `flat` (control: the literal without a block) | balanced | balanced |
| `block_with_local` (refused, pinned) | 20 / 0 | 20 / 0 |

`TestSelfHostVblockStructLocal{X86_64,Arm64,Wasm}` holds each row to the
interpreter's answer under both lowerings, and runs the x86-64 build under
`FERN_SANITIZE=1` too.

## Re-pinned: `elem_value_block`

#10573's refused threader row `elem_value_block` builds exactly this shape
(`let h: Holder = { Holder { x: acc[0] } }`). Its census moves from 36 / 13 to
36 / 24. The 11 new frees are `h`, released in `walk` on the 11 calls that
take the branch. The generated asm for `walk` now calls
`__struct_drop_Holder` as its flat twin `elem_struct_field` does, after the
same `__fern_rc_inc` of `acc[0]` that builds the Holder. `main` still takes
only the shallow `__fern_arr_dec`, so the threader stays refused. The 12
blocks left live are the 12 its flat twin leaves (26 / 14). The row is clean
under `FERN_SANITIZE=1`.
