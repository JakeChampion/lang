# Nested match statement checks

Source: `d2980e0e6575e57ecdbdc908b22a92a6f409a21d`.
The source includes main commit `ebc264f8c61eed3073080e69fc6508d8b2930c8d`.

A nested match with no final value remains a statement in both parsers.
This rule also applies inside callback bodies.
The parsers retain their depth limits during recursive checks.
The formatter keeps the callback body and preserves its result type.

The source patch and file hashes identify the measured inputs.
The logs retain each package result and each command result.
The bootstrap record identifies the official producer and all three stages.
Stages 2 and 3 have identical bytes.

The runtime checks cover explicit and inferred callback result types.
The callback checks run on x86-64 Linux, ARM64 Linux, and WASI.
Darwin images are built but are not executed locally.
All runtime allocation counts must balance.

The earlier upstream integration records a separate source scope.
Its format check failed before this fix.
The failed trial also records a missing recursion guard.
Those failures remain in the local records.
