# A producer rebind keeps the struct-array credit

2026-09-27 — `irlower`: `struct_arr_producer_rebind`,
`emit_struct_arr_reclaim_store`. Fixes #10360, splits off #10420.

```fern
struct Inst { name: string, depth: i32 }
function mk(n: i32): Inst[] { var out: Inst[] = []; out = out.append(Inst { name: "g" + "", depth: n }); return out; }
function main(): i32 { var pending: Inst[] = []; pending = mk(1); return pending.len(); }
```

On the AST lowering this leaked the one `Inst` box (allocs 3, frees 2). The
STRUCTARR and ARRSTRUCT credits, which free a struct array's element boxes,
refused every reassignment except a self-append. So `pending` took the shallow
buffer dec at exit. The same program with `var pending: Inst[] = mk(1)` was
balanced, because a producer init already earns the credit.

## The rule

A rebind `name = g(..)` to a producer registered under `STRUCTARRF:` /
`ARRSTRUCTF:`, which reads `name` at most as a borrow, is now sanctioned by
`structarr_unsafe_for` and `arrstruct_unsafe_for`. This is the struct-array
counterpart of `strarr_rebind_is_fresh`. The producer's elements are its own,
so the new array shares nothing with the old one. The assign path then stores
through `emit_struct_arr_reclaim_store`, which frees the old array whole under
a cow guard. That is the helper a loop re-declaration already used, and both
sites now share it. An empty `[]` seed that the body rebinds from a producer
is collected as a candidate, as an append-built one is. A local that shadows
the producer's name drops its rows (`unshadowed_producer_rows`).

## Measured

`TestSelfHostStructArrProducerRebind*` has six rows on x86-64 (leakcheck and
sanitizer), arm64 and wasm, each on the semantic and AST lowerings. On main
the AST legs of the five balanced rows leaked every element box. They now
balance. The sixth row binds an element of the old array, which refuses
the credit, so it is held to the sanitizer only.

## Not covered

#10420: an accumulator threaded through a call that hands its argument back
(`pending = walk(fd, pending)`, where walk returns `acc` or
`acc.append(..)`). Its result is not a producer's, so no credit can be granted
without a proof that walk threads its parameter. The #10357 rows
`struct_elem_walk` (89 allocs / 73 frees) and `struct_elem_scope` have this
shape and stay pinned, now under #10420.
