# semreach absorb reads the functions that each instruction names

`semreach.absorb` runs after each walk of a function.
For each instruction with a name, it found the named function by name, or each arm of a dyn call.
Then it scanned each value of the function for a map type, and built and found the names of the key functions of that map.
None of this depends on the walk.

`names_of` now records, once for each function, the functions that each instruction names, by the result value of the instruction.
It also records the key functions of the map types of the function.
`absorb` reads these tables.
`reached_name` and `named` are deleted.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,491,687,440 |
| this change | 29,476,013,364 (−0.05%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
