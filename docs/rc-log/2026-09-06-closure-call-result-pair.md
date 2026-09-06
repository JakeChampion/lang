# 2026-09-06 — a closure that arrives as a call result (#8622)

The third provenance for a closure-pair slot, after #8545's per-closure thunk
arm and #8546's generic arm. Both of those gate on `pairSlot`: the slot was
written by an `OpMakeClosure`, or aliases one, to a fixpoint. A closure
returned from a function has neither, so the rewrite declined it and the env
box was never released.

```fern
function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var add5 = makeAdder(5);
    return add5(37);
}
```

`allocs=2 frees=1`, 16 bytes — one per closure returned, unbounded in a loop.

## Ownership was already established, which the issue got wrong

`#8622`'s body says the blocker is that nobody knows who owns the returned
pair. `FERN_RC_TRACE` says otherwise:

```
a 0000000010000000 0000000000000010   <- env box,  16 B  — never freed
a 0000000010000010 0000000000000030   <- pair,     48 B
f 0000000010000010 0000000000000030   <- pair freed
```

Two allocations, one free, and the free is the PAIR. A drop was already
running on the local and reclaiming it — the generic `__fern_closure_drop`,
which frees the block it is handed and nothing behind it. The env is reachable
only through that pair, so once the pair is uniquely held and being freed the
env is the pair's to release. The question was never ownership; it was that
`pairSlot` could not see the slot was a pair.

## The fact, and where it comes from

`ElideClosurePair` has the whole `Program`, so the callee's own signature
answers it: a `Func` whose `ReturnType` is a `*ast.FuncType` hands back a
pair. No plumbing of an analysis result, no key-space question between AST
names and emitted symbols — the index is built from `prog.Funcs` in the same
pass that consumes it.

A slot written by a call is already `failed` for elision (its readers cannot
be collapsed), so it keeps its pair and the drop rewrite is free to fire.

## Measured

| case | before | after |
|---|---|---|
| `closure_adder` (census) | 1 | **0** |
| `closure_escapes_return` (x86-64 / arm64 / wasm) | 16 / 16 / 16 | **0** |
| `string_closure_capture_churn_free` | 3200 / 6400 / 3200 | **0** |

Two rows did NOT move — `generic_fn_arg_capturing_lambda` (2) and
`closure_capture_passed_to_owned_param` (64 / 80 / 64). That is the point.
The unsound name-keyed rewrite recorded in
`2026-09-05-closure-pair-generic-drop.md` zeroed all five, and was dispatching
through non-pairs to do it. A change that fixes exactly the provenance it
reasons about, and leaves the others alone, is the shape to expect.

Controls held: `slice_views` (no closure in it) runs clean on wasm rather than
trapping, and the #8637 cycle still leaks 32 bytes at exit 0 rather than
recursing.
