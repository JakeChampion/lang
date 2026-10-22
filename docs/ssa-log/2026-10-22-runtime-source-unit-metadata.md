# Native runtime source unit metadata

Status: source-unit metadata for #2649.

The native runtime source emitters previously used the outer unit's metadata.
Calls within a runtime source therefore used stack entries even when the callee defined a register entry.

Both emitters now prepare register entries, the floating-point ABI and SSA leaves from each runtime source.
They reuse the prepared SSA bodies and restore the outer unit's metadata after emission.
This change supports the remaining runtime migrations in #2649.

Validation:

- The missing-file probe checks register calls to the path-copy, error and UTF-8 helpers on all three native targets.
- The probe executes on x86-64 Linux and ARM64 Linux through QEMU.
- The x86-64 probe preserves the error result with and without the sanitizer.
- A synthetic runtime source checks floating-point register calls and restoration of the outer register, float and leaf tables.

Runtime helper emission, dependency closure, file I/O, I/O errors, close errors and portable tasks pass the targeted suites.
These suites cover x86-64 Linux, ARM64 Linux through QEMU and WASI.
The targeted run took 220 seconds and had no skipped tests.

The same tests fail against the prior emitters.
All nine register-call checks fail across the three native targets.
The floating-point call check also fails.

The metadata change exposed an x86 operand cycle in the byte-copy runtime.
The stack adapter previously used six stack instructions for that cycle.
The adapter now resolves register cycles with register exchanges.

An executable probe checks 625 assignments across four registers and one spill slot.
The assignments include register cycles, repeated sources and spilled values.
The byte-copy suites pass on x86-64 Linux, ARM64 Linux through QEMU and WASI.
These suites include bounds failures, alias cases, sanitizer checks and heap census checks.

The corrected compilers share base commit `d669f557c` and the same stage0 pin.
Across 37 benchmarks, none of the 74 native emission pairs gains instructions.
Thirteen native pairs lose instructions.
All 37 WASI outputs are byte-identical.
The prior adapter also fails the executable operand probe.

Callgrind compares the two byte-copy benchmarks and the two UTF-8 ingest benchmarks.
The `byte_copy_with` run executes 3,000 more instructions, a 0.0060% increase.
The other three runs execute fewer instructions.
These instruction counts do not measure elapsed time.

The current validation does not establish a speed improvement.

Lint passes.
The compiler, standard library and 72 driver source checks pass.
The fixture source check passes.
The initial feature census failed because #12993 added a generic sort function and its comparator lambda.
The corrected counts are 16 generic functions and 15 arrow lambdas.
The corrected census and per-module runtime tests pass.

Bootstrap stage 2 equals stage 3 at 14,062,608 bytes.
Their SHA-256 is `3ed9f5835ae61aa7c163911c3fb2e8e33f2a7ecef7a8ffc3265fb7dfd4de11a9`.
