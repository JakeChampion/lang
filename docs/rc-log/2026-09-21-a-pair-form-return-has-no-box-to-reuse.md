# A pair-form return has no box to reuse

2026-09-21 — `ir.consumingReuseCtor`. Closes #9901.

## What was wrong

```fern
enum Box { Full(i32[]), Empty }

@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) {
        Full(xs) => { return Full(xs.with(i, x)); },
        Empty => { return Empty; }
    }
}

function main(): i32 {
    var b: Box = Full([1, 2, 3]);
    var keep: Box = b;
    b = put(b, 0, 9);
    match (keep) { Full(xs) => { return 10 + xs[0]; }, Empty => { return 1; } }
}
```

`keep` names the box across the call and nothing writes through it, so the
answer is 11. Every compiled backend answered **19** — `put` wrote its 9 into
the buffer `keep` was still holding. The AST interpreter answered 11
throughout, so the two disagreed and no lane reported it.

## Why

A consuming (`own`-param) match arm whose body is `return Ctor(..)` of the same
enum is reuse-paired (C2, `computeConsumingMatchReuse`): instead of freeing the
scrutinee's box and buying another, the arm hands the shell to that
construction. The pairing moves the arm's whole box release into the token the
construction emits — the uniqueness test, the shallow free on the unique
branch, and on the shared branch the retains that make each moved-out payload a
counted owner (#6720). `emitConsumingMatchBoxFree` is skipped precisely because
the token is going to do it.

`Box` is Option-shaped, so `put` returns the **pair form**: `(tag, payload)` in
registers, no box, no `__alloc_reuse`, no token. The release was emitted
nowhere at all. What the arm lowered to was an extract and a `.with`:

```
 12 local.load 3 / const 8 / add / load        ; xs = box.payload
 22 local.store 7
 23 local.load 7 / rc.is_unique                ; the `.with` copy-on-write
 29 call __fern_arr_cow_inplace
```

One uniqueness test, on the array; none on the box, and no retain. `xs` was an
uncounted alias of a payload whose box had two holders, so the array's count
said 1 and `.with` wrote in place.

The same function without `own` emits what it should, because the
owned-by-default path (`emitOwnedConsumingArmDrop`) is not reuse-paired:

```
 19 local.load 4 / rc.is_unique                ; the box
 21 if    -> __fern_box_free                   ; unique: shallow free
 26 else  -> rc.inc(xs); rc.dec(box)           ; shared: retain the payload
```

## The fix

`consumingReuseCtor` declines a construction that lowers as a pair-form return.
The arm then takes `emitConsumingMatchBoxFree`, which is the general path and
has both branches. One edit covers the analysis and the lowering, since both
ask through that function.

It is asked WITHOUT the `len(b.defers) == 0` condition the return lowering
itself carries. `b.defers` fills during lowering and is empty during analysis,
so including it would let the two phases disagree — and disagreement here is
what produced the bug. A deferred function boxes its return and merely loses
the reuse.

## What it also fixed

The pairing took the release with it, so the arm leaked its box: a pair-form
consuming traversal lost one box per call. 5000 iterations moved the bump
allocator past the 512-byte bound (exit 98); they are now flat.

## Traps

- **`fern --run` is not the interpreter.** It links a temporary binary and
  executes it, so the issue's table read as "interp disagrees with native" when
  both rows were the compiled path. `-interp` is the AST interpreter, and it
  was right all along — which is what makes the differential harness
  (`assertNumProgramAgrees`, oracle = interp) the correct gate.
- **The guard is not what was broken.** E051 admits `b` here through the
  self-reassign shape (`b = put(b, ..)`): the target is overwritten by the
  call, so `b`'s old value is dead after it. That reasoning is about `b` and
  says nothing about `keep`. But the runtime machinery already exists to cover
  exactly that — the uniqueness test the shared branch turns on — and closing
  the guard instead would have left the release missing and the leak in place.
  Pay at run time; the guard is not load-bearing.
- **Two of the four differential cases pass either way**, on purpose: an
  unaliased box must still update in place (a fix that copied unconditionally
  would pass the two aliasing cases), and the same shape on an enum too wide
  for the pair form must keep its reuse.
