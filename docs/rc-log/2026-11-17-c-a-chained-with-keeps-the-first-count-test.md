# A chained with keeps the first count test

2026-11-17. All targets. The change is in `ssaunits.chained_withs` and
`ssarc.linked_base`. It is the second part of #15747.

## The shape

```fern
@noinline
function put3(xs: i32[], at: i32): i32[] {
  return xs.with(at, 1).with(at + 1, 2).with(at + 2, 3);
}
```

`xs` is a borrowed parameter. The first `.with` tests the count of its box.
It writes a unique box in place, and it copies a shared one. Each later
`.with` compared its receiver with the box of the parameter. When the two
were the same box, it tested the count again and kept a copy path.

That second test always passes. The receiver is the box of the parameter
only when the first `.with` found the box unique. No instruction between
the two updates can add a reference.

## The rule

A `.with` is chained when all of these are true:

- Its receiver is the result of an earlier `.with` of the same block.
- The two updates have the same link root.
- No instruction between them reads the root or a value linked to it.
- No index or element of the two updates is the root or is linked to it.

`linked_base` gives a chained `.with` its receiver with no count test and
no copy path. An update that is not chained keeps the test.

## Measurement

Lifted SSA of `put3` on x86-64:

| | Before | After |
| --- | --- | --- |
| Calls of `__fern_rc_is_unique` | 3 | 1 |
| Calls of `__fern_arr_slice_u32` | 3 | 1 |
| Blocks | 18 | 8 |
| Unproved values | 5 | 1 |

The rows of `.github/certify-baseline.txt` do not move. The compiler has no
chain of this shape on a borrowed parameter.

Witnessed: `TestSelfHostBorrowedWithChainCopiesOnlyTheParameter` counts the
calls on x86-64 and arm64. Three new rows of `borrowedWithChainRows` run on
x86-64, arm64 and wasm32 against the interpreter, with the leak census. One
row reads the parameter between the updates, and one gives the first result
to a callee that keeps it.

## Next lead

The bound tests of `at + 1` and `at + 2` are still two tests. This is the
last open item of #15747, and it belongs in the IR.
