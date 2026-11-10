# A typed string const reads as its literal

The parser substitutes the value of a const at each use when the const has no type.
A typed const was substituted only when its value is an `i32` literal or a boolean.
A typed string const, such as `const PUNCT_BYTES: string = "+-*/…";` in the lexer, stayed a call to its const function at each read.

The inliner splices that call into a small caller.
It gives up on a large caller, such as `lexer.tokenize_impl`.
There, each pass of the punctuation loop in `single_punct` called the const function and released the string that it returned.
In one compile of the subject, that loop ran 6.4 M times at 16 instructions each.

A const of type `string` whose value is a string literal is now substituted at each use, as the `i32` and boolean consts are.
`TestSelfHostTypedStringConstIsItsLiteral` names typed string consts in its own module and across an import.
It pins that this program emits the same code as the program that writes the literals.
With the old rule, the test fails.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,376,616,406 |
| this change | 29,295,650,749 (−0.28%) |

The emitted code changes, because the sources of the compiler use typed string consts.
`TestFernFixturesSelfHostX86_64` and `TestFernFixturesSelfHostWasm` pass.
