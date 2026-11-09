# A descriptor handle is named by four literal compares

`semtypes.is_fd_handle` tests whether a type is one of the built-in descriptor types: `Reader`, `Writer`, `Dir` and `Socket`.
`semtypes.is_handle` calls it, and the checker compile calls `is_handle` 6.35 M times, mostly from `ssasem.reference` and `semrecords.resolved`.
It looped over the array that `fd_handle_names` returned and called `__fern_str_eq` for each name.
The three checker sites tested the same array with `util.has_str`.

`semtypes.is_fd_handle_name` now compares the name with the four literals.
A compare with a literal tests the first byte inline, so most names fail without a call.
`is_fd_handle` and the three checker sites use it, and `fd_handle_names` is gone.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from 7b460dce.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| 7b460dce | 29,867,954,834 |
| this change | 29,790,283,971 (−0.26%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
