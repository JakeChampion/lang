# 2026-09-27 — a borrowed parameter read again is not grown in place (#10353)

```fern
function f(b: i32[]): i32 {
    let c: i32[] = b.append(9);
    return b.len() * 10 + c.len();
}
```

With a 5-element `b` that has spare capacity and a caller holding no binding
of its own, the semantic lowering printed 66 where the interpreter, native and
the AST lowering print 56. `deferred_retain` holds a borrowed parameter's
retain back so the non-consuming push can grow the box in place, and its
soundness argument covered only the caller: `bracketed` holds a count across
the call when the caller still reads its binding. It did not cover this frame
reading the parameter after the push. The retain supply could not say so,
because a borrowed parameter is never owned and is supplied by retain whether
or not it stays live.

#10351 extended the in-place path along an append chain, which moved the
two-push form of the reproducer from one wrong answer (679) to another (778);
574 is right.

## The change

`ssaunits.plan` already walks each block backwards with a liveness set that
takes dependents into account, so a read through a view of `b` counts as a
read of `b`. It now records, per append or with result, whether the receiver
is live after the instruction (`Plan.kept`), and `deferred_retain` declines a
borrowed parameter that is. Such a push keeps its retain up front and copies,
which is what keeps the parameter's value whole.

## Cost

The rule is conservative: a push only changes the length and the slots past
it, so a frame that goes on reading only the elements it had would have been
safe in place. Those sites now copy once. The chain and threading shapes that
motivated the in-place path read the parameter no further, so they keep it:
`chain_fill(100)` still copies nothing.

## Pinned

`reread_one` and `reread_chain` in `TestSelfHostSemanticSourceRC`, on all four
legs. With the `kept` test removed, the arm64 leg fails on program output.
