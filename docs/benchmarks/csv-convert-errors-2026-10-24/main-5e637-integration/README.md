# CSV errors, typed conversions and generic supertraits

Final source: `7d0712dedd1a487d7a4eac847d383349d9b14f48`.
The machine checkpoint is `e944a2767c385d0ac3c650a87d8b6369aec3e6b3`.
Only parser comments and the trait guide differ between these revisions.
A fresh three-stage bootstrap produces identical binaries at both revisions.
The source uses the completed codec and system API changes as dependencies.

CSV parse functions return typed errors with a byte offset and an error kind.
The parser rejects malformed quotes and unexpected record separators.
It preserves quoted fields, escaped quotes, document separators and different row widths.
The canonical CSV names replace the old module prefixes.
Conversion traits preserve the concrete error type through `Result`.

The checker preserves trait arguments on associated calls (#13120).
Both parsers and formatters preserve generic parent arguments (#13121).
The checkers substitute arguments along parent edges for associated functions and receiver methods.
They reject a parent implementation with different arguments.
Tests cover nested results, imported traits, aliases, transitive parents, generic implementations and default methods.

The final Go suites run 2514 cases with seven deliberate AST walker exclusions.
The compiler suites pass 732 cases with no failures or skips.
The formatter suite passes 266 cases.
This formatter run excludes the optional whole-corpus type-check test, which exceeded the earlier ten-minute limit.
The failed combined command also used an incorrect package path.
Its log remains diagnostic evidence, separate from the final successful suites.

The focused suites pass 105 cases.
All 11 CSV and conversion tests pass.
The CSV IR suite passes seven cases against the actual standard module.
The final canonical format check covers all 1200 repository source files.
Go vet and Go format checks pass.

The matrix passes all 55 commands for CSV, two conformance programs, conversions and generic inheritance.
Both checkers and interpreters pass.
All four target builds pass.
Native x86, ARM64 under qemu and WASI executions produce the expected output.
All 15 allocation checks balance with zero live bytes.
Darwin is a compile check only.

The conformance and conversion wrappers check the original main results before they return zero.
This preserves the numeric assertions when the WASI component maps a nonzero result to process exit code 1.
The fixture manifest records both original and wrapper hashes.

Bootstrap stages 2 and 3 are byte identical:
`ff575b4d7428fff125c751c505a1c72fda572405057228ced0631ba8435b5471`.
All 11 strict driver size gates pass against the unchanged main baselines.
The largest increase is 0.2 percent.

The artifact manifest records 496 source hashes and all evidence hashes.
The matrix archive contains all 110 output and error logs.
The complete patch applies in reverse to the source revision.
The final bootstrap stages and oracle remain in the workspace.
