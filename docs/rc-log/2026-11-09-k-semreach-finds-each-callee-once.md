# semreach finds each callee and argument record once

`semreach.walk` iterates over the blocks of a function until no value changes.
`semreach.reach` walks a function again when the facts that it reads change.
In one compile of the subject, it walked functions 10,930 times.

Each pass found the callee of each call by name.
Each pass also built the type key of each call argument to find its record.
These two lookups made 965 K calls to `util.NameIndex.find` in `walk`, and neither answer depends on the pass.

`keys_of` now records the callee of each call, with the result value of the call, as it records the other keys.
Every semantic instruction has a result, so each call has a slot.
`records_of` records the record of each call argument.
`call_value` and `none_held` read these two tables.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,663,147,764 |
| this change | 29,539,970,826 (−0.42%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
