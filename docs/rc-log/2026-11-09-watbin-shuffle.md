# WAT shuffle index syntax

The SIMD lane gate failed when a shuffle index contained `x`.
The generic integer parser exited before the shuffle-specific diagnostic.
The same decimal-only check rejected valid hexadecimal and separated index spellings.
Wasmtime 50.0.1 accepts those valid forms.

The assembler now parses each index with its unsigned WAT spelling rules.
It accepts a leading plus, decimal or hexadecimal digits, and underscores between digits.
It rejects negative zero, uppercase hexadecimal prefixes, misplaced underscores, missing digits, and values above 31.
The parser checks the range after each digit, so an arbitrarily long token cannot overflow its accumulator.
Malformed operands retain the shuffle byte-index diagnostic.

Six alternate spellings pass independent Wasmtime execution and the assembled binary tests.
The cases exercise flat and folded shuffle forms.
Twelve malformed or out-of-range controls report the required category.
The 18 flat assembler controls and the repository complexity ratchet pass.
The supported compiler build and bootstrap smoke checks pass.
Full CI remains required.

Issue: #14787. Parent: #14566. Audio gates: #14509.
