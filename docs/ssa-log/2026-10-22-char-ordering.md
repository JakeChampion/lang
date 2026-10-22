# Character comparisons use Unicode scalar values

Issue: #12831.

Both checkers now accept `<`, `<=`, `>` and `>=` between two characters.
The typed SSA operator contract accepts these comparisons too.
The existing integer comparison instructions compare their scalar values on each target.
Mixed character and integer operands still produce E009.

The new `char_ordering` conformance case compares every pair of eight scalar values.
The values include NUL, both sides of the surrogate range, and the largest Unicode scalar.
A function with `@noinline` keeps the comparisons in executable code.
The existing generic character comparison and character collection cases also pass.

Validation:

- Both interpreters and the x86-64, ARM64 and WebAssembly fixture legs pass.
- The complete Go checker, parser and monomorphiser suites pass.
- The full checker-code differential and the complexity ratchet pass.

The character Ord implementation retains its casts because the pinned stage0 does not accept the new operators.
