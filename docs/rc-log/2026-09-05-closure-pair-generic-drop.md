# 2026-09-05 — the generic arm of the closure-pair drop (#8546)

`2026-09-05-closure-local-pair-release.md` routed the non-elided closure
slot's `__closure_drop_<name>` through `__drop_closure_value`. That covered
one of the two drops `emitDec` can choose. The other is the generic
`__fern_closure_drop`, which it picks whenever the closure has **no**
rc-tracked capture — and on a pair that helper frees the block it is handed,
which is the pair and never the env behind it.

So a lambda capturing one scalar BY VALUE still leaked its whole env box on
every call that took it as an argument. #8546 found it independently with a
sharper reduction than #8545 had:

```fern
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    var sink: i32 = 0;
    var add = (x: i32) => sink + x;
    return apply(add, 4) - 4;
}
```

Both arms now route to `__drop_closure_value` — but the generic arm is gated
on the slot PROVABLY holding a pair, which is the whole difficulty here.

## The axis, measured

x86-64 `-sanitize`, #8546's reductions plus three cut across the two candidate
axes:

| shape | before | after |
|---|---|---|
| read-only i32 capture, `(i32) => i32`, passed | `allocs=2 frees=1` 16 B | 0 |
| read-only i32 capture, `(i32) => void`, passed | `allocs=2 frees=1` 16 B | 0 |
| mutated i32 capture (cell), passed | 0 after #8545 | 0 |
| read-only string capture, passed | 0 | 0 |
| any capture, called directly (elides) | 0 | 0 |

The axis is the CAPTURE KIND, not the return type and not the arrow spelling:
only a closure whose captures are all non-rc-tracked took the generic arm.
Identical on arm64 and wasm.

## `__fern_closure_drop` is not closure-specific

The first cut keyed the rewrite on the helper NAME. It broke two ways at once,
and both are worth recording because the name reads as if it were specific:

- `genArrOfArrDropFn` reuses `__fern_closure_drop` as the per-element drop for
  an ARRAY OF ARRAYS. `slice_views` contains no closure at all, and it trapped
  with `indirect call type mismatch` — `__drop_closure_value` dispatching
  through word 2 of something that is not a pair.
- The generated reclamation helpers end with `LoadLocal 0;
  __fern_closure_drop` over an env they have ALREADY dispatched through.
  Rewriting one makes it call the pair-dropper on itself: unbounded recursion,
  and every closure program segfaulted rather than leaking.

The gate is now `pairSlot` — the slot was written directly by an
`OpMakeClosure`, or is an alias of a slot that was, to a fixpoint. That is a
statement about the VALUE rather than about the helper's name, so neither
shape can be reached.

## What this does NOT fix

A closure that arrives as a CALL RESULT keeps its leak. `closure_adder` still
measures `allocs=2 frees=1`, 16 bytes:

```fern
function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
```

Its slot has no `OpMakeClosure` writer in `main` and is not an alias of one,
so `pairSlot` declines it. `closure_adder` (1),
`generic_fn_arg_capturing_lambda` (2) and the corpus's
`closure_escapes_return` (16 B), `string_closure_capture_churn_free`
(3200 / 6400 / 3200) and `closure_capture_passed_to_owned_param` (64 / 80 / 64)
all pin that class and are unchanged here.

The name-only cut appeared to take every one of those to 0. That is the trap:
it was also dispatching through non-pairs, so its zeroes are not evidence the
release was right — only that something was freed. Whoever closes the
call-result class should establish who owns the returned pair (the callee's
transfer inc, or the caller) before reading a zero as a fix.
