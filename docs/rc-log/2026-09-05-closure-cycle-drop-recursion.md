# 2026-09-05 — a closure cycle crashed instead of leaking (#8637)

#8545 made the per-closure `__closure_drop_<name>` thunk reachable for a
closure LOCAL that does not elide. On a cyclic closure graph that turned a
documented safe-leak into a stack overflow, on all three backends.

```fern
function main(): i32 {
  var g: () => i32 = function (): i32 { return 1; };
  var f: () => i32 = function (): i32 { return g(); };
  g = f;
  return 0;
}
```

| commit | result |
|---|---|
| `6bb4c36` (parent of #8545) | `leak 48 bytes in 2 blocks`, exit 0 |
| `e7c07d3` (#8545) | **SIGSEGV**, exit 139 — arm64 139, wasm trap 134, interp signal 11 |
| with this fix | `leak 32 bytes in 1 blocks`, exit 0, on all three |

## The loop, read off the stack

```
#0  __fn___drop_arr_closure
#1  __fn___closure_drop___closure_lambda_2
#2  __fn___drop_arr_closure
#3  __fn___closure_drop___closure_lambda_2
```

`BoxMutatedCaptures` boxes the mutated capture `g` into a one-element array
whose element type is a closure. `arrElemStructDropName` maps that to
`__drop_arr_closure`, which dispatches through each element's drop-fn pointer
— and after `g = f` the element IS the pair being released, whose drop-fn is
the thunk doing the releasing. Every `is_unique` gate passes at every step,
because on a cycle the counts are precisely what is wrong.

`__drop_closure_value` is not in the loop. #8545 did not create the
thunk ↔ `__drop_arr_closure` hazard; it made the thunk reachable for a local
where the generic env-only drop had been.

## The wrong turn, which is the finding

The first fix gave the thunk's array-capture arm the flat
`__fern_drop_arr_ptr` instead of `__drop_arr_closure`. The recursion stopped
and the program reported

```
fern-sanitizer: rc over-release (double free)
exit=124
```

Same lesson as the crash, one level down: once the thunk runs on a cyclic
graph the counts are already inconsistent, so ANY release is wrong — a
dispatching one recurses, a flat one over-releases. There is no per-element
policy that makes a cycle collectable.

So the thunk now leaves alone any capture that IS or CONTAINS a closure
(`capturesAClosure`). The env block is still freed by the thunk's tail; the
captured closure is not touched. A cycle leaks, which is what #8440 documents
and what the generic drop did before #8545 reached this shape.

32 bytes rather than 48: the pair is freed now and only the env plus the cell
survive, so the cyclic case gives back MORE than it did before #8545 while
still leaking.

## Gated

`closure_cycle_leaks_without_crashing` — 50 rounds, pinned at 1600 bytes on
x86-64, arm64 and wasm alike. Against the #8545 compiler it dies with a
signal, which the corpus reads as a crash rather than a verdict; before #8545
it leaked 3200 where it now leaks 1600.

## Trap

Every gate stayed green through the regression — rc corpus, census,
`TestFernFixtures`, `TestSelfHostStdTestE2E`. None of them contains a cyclic
closure, because E049 is supposed to prevent one and #8440 is the open hole
that it does not. A corpus with no case for a shape the checker is believed to
exclude cannot notice when that shape starts crashing.
