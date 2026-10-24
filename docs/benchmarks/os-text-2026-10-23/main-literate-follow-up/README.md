# Literate library test repair

Main run `40100964885` failed after the literate library pilot.
The documentation test omitted modules with the `.fern.md` suffix.
The playground test built an x86 driver on ARM hosts.

The documentation check now tangles literate modules and uses their canonical module names.
The playground test builds its driver for the current host.
Process failures also report the signal error text.

All 26 affected test cases pass without skips.
An ARM64 driver also passes the same program through check, interpreter, and compiler modes under QEMU.
Its generated WebAssembly runs and returns the expected value, 42.

Only three Go test files differ from the measured OS source.
The compiler, standard library, and bootstrap pin remain byte-identical to the prior measured scope.
The identity record and patch preserve this distinction. The prior runtime and bootstrap evidence remains in its original scope.
