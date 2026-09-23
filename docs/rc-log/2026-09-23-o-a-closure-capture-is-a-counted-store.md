# A closure capture is a counted store

2026-09-23. Native. #10112.

```
function make(s: string): (i32) => i32 { return (x: i32): i32 => { return x + s.len(); }; }
…
while (i < 200) { var f: (i32) => i32 = make("a" + "b"); … }
```

On x86-64 `-sanitize` this freed 2 of 400 blocks. A closure that captured
a local built from the parameter, rather than the parameter itself, freed
everything.

## Cause

`f` was `freeEligible` but had no per-trip drop: `preciseDropTarget`
refused it in `initMayAliasLive`, because its initialiser is a call with
a pointer argument in a position `paramCountedRetain` did not credit. The
same missing credit kept `countedArgTemp` from releasing the fresh
argument after the call. So the closure and the string it held lived
until the exit sweep, which runs once.

The retain summaries (`stringParamCounted`, `arrayParamCounted`,
`paramProjectionsSafe`) counted a closure capture of the parameter as an
occurrence and never marked it safe. It is a counted store: MakeEnv
retains a borrowed capture through `emitAliasInc`, and the closure's drop
thunk releases it. It is the same argument the construction-slot arms
make, and each summary now has a `MakeClosure` arm beside them.

A dyn parameter was never affected. The summary treats it as holding no
heap and credits it outright.

## Measured

Six trips per shape, `-sanitize`, x86-64 and arm64:

| parameter captured | before | now |
|---|---|---|
| a fresh string | 2 frees of the whole run | balanced |
| a live record | leak per trip | balanced |
| a fresh array | leak per trip | balanced |
| a dyn value | balanced | balanced |

wasm answered the same both ways. `TestClosureCapturingAParameterReboundInALoop`
holds the rows. `TestDynCaptureBesideOtherCaptures` now rebinds its
closure in the loop body, the shape it avoided for this bug.
