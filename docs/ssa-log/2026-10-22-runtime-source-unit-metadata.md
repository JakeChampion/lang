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

The current validation does not establish a speed improvement.
