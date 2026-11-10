# The inliner skips an attempt that cannot change

This is a slice of #11303: a repeated walk does less work.

`seminline.splice_rounds` runs rounds until no function changes.
Each round, `splice_round` calls `splice_calls` on each function that has a ready callee.
In one compile of the subject, it made 4,980 attempts in 46 rounds.
Only 2,002 attempts changed the function.
An attempt costs about 60 K instructions.

An attempt reads the caller, each callee with its flags, and the words of the callees of each callee.
The flags are `ready`, `ctors`, `shared`, `small`, and the returned-word flag.
`Tries` now records two events for each function:

- `stamp` is the last event at which the body or the flags of the function changed.
- `tried` is the last attempt on the function that changed nothing.

`splice_round` skips a function when its `tried` event is newer than each callee `stamp` that the attempt reads.
The inputs of the attempt are then the same, and the attempt cannot change the function.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 28,579,812,392 |
| this change | 28,532,077,901 (−0.17%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
