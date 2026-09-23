# A parameter alias with a counted escape is a borrow

2026-09-23. Native. #10113.

```
function tag(src: string, i: i32): i32 {
    var x: string = src;
    var f: (i32) => i32 = (k: i32) => x.len() + k;
    return f(i);
}
```

On x86-64 `-sanitize` this leaked one string per call.

## Cause

The borrowed-parameter leg of `computeBorrowedAliases` (#9244) cancels
the transfer inc of `var x = src` when `x` only ever reads through the
value. It asked `bindingConfinedToArm`, which counts any other use as an
escape, and a closure capture is one. So the binding kept its inc, and
the exit sweep, which skips an ineligible string, never released it.

A capture MakeEnv retains is a reference of the closure's own, and the
caller holds `src` across the whole call. So `x` needs no count of its
own while every escape of it takes one. The leg now asks
`bindingReleasableInArm`, the counted-escape reading the match-binding leg
already uses. `bindingUsesExcused` gains a `MakeClosure` arm that excuses
a capture which takes its retain, so a move into the env stays an escape.

## Measured

Four calls, `-sanitize`: 4 of 8 blocks freed before, 8 of 8 after, on
x86-64 and arm64. wasm answered the same both ways.
`TestBorrowedParamAliasWithACountedEscape` holds it.

## Found on the way

An ineligible string local that takes a counted reference, such as `out`
in `out = x`, is never released. The exit sweep skips an ineligible
string, and the two-word ABIs have no non-freeing string release to
give it (#10117).
