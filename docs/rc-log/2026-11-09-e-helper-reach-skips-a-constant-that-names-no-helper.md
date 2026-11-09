# Helper reach skips a constant that names no helper

`semhelperreach.select` keeps each typed drop helper that a kept body can reach.
A constant struct or a dynamic dispatch can name a helper inside its encoded string.
So for each such op, `references` tested every helper name with `util.contains`.
The fixed checker compile has 579 helper names and 3,887 constant structs.
The test ran 1.5 M times.

Each helper name contains `__`, but only 1,142 of those op strings do.
`select` now finds an infix that each helper name contains: `__`, or the empty string when one name does not contain it.
`references` tests the helper names only for an op whose string contains that infix.
An op string without the infix cannot contain any helper name, so the result does not change.

`TestSelfHostTypedHelperReachability` now has three more cases:

- A constant that names a mangled helper keeps it and its callee.
- A constant without `__` keeps no mangled helper.
- A helper name without `__` is still found in a string without `__`.

The earlier cases use helper names without `__`, and they fail when the infix is always `__`.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,788,699,847 |
| this change | 29,653,462,970 (−0.45%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
