# Restore scalar terms in proof diagnostics

Status: [record] diagnostic repair from 2026-11-09, for issue #14566.

Commit `d409ac1f35` moved the scalar predicate to `semtypes.concrete_scalar`.
It also replaced the word `scalar` inside seven diagnostic strings in `ssacreditabi`.
The existing typed-storage controls require the original diagnostic text.

The first failed control printed this message:

```text
replace: require: assertion requires two semtypes.concrete_scalar parameters expected replace: require: assertion requires two scalar parameters
```

The driver then called `exit(91)` before it released its live test data.
The exit census reported 5,678 live blocks and 378,560 live bytes.
That early exit does not establish a leak on normal completion.

The repair restores the seven diagnostic strings.
The predicates and proof controls remain unchanged.

The typed physical extraction, wrapper continuation, and storage invocation tests pass on all three targets.
Those targets are x86-64 Linux, ARM64 Linux under QEMU, and WebAssembly.
All nine cases report balanced allocation counts and zero live bytes.
The test command completes in 410.149 seconds without skips.
The module source check and format check also pass.
