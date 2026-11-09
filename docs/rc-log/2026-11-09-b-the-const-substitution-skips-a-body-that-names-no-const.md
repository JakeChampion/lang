# The const substitution skips a body that names no const

`parser.substitute_const_refs` replaces each use of a substituted const with the value of the const.
It runs once for each module in the parser, and once more on the merged module in `flatten.bundle_with_prefixes`.
Each run rebuilt every function body through the map walk of `astwalk`.
On the fixed checker compile, the two runs replace 125 uses in all.

The walk changes only an unbound identifier that names a const in the table.
`astwalk.names_any` now tests each body for such a name with a read-only fold.
A body that names no const keeps its statements, and only the defaults of its parameters go through the substitution.
The fold visits the same expressions as the map walk.
So each body that the walk can change is still rebuilt.

`TestSelfHostConstInLambdaIsItsLiteral` parses a const that one function names only inside a lambda.
It checks that the use becomes the literal `7`, and that a function without a const keeps its lambda.
The test fails when `names_any` finds no name.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from main at 15a62e41.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| main at 15a62e41 | 30,201,033,178 |
| this change | 30,008,037,988 (−0.64%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
