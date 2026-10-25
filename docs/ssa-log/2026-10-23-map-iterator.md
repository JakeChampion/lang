# Map entry iterators

Issue #12812 requires Map.iter to return an Iterator of key and value tuples.
The method returns an owned ArrayIter snapshot from Map.entries.
Normal source method dispatch supplies its type, its body, and its Iterator implementation.
The two interpreters run that source body.
Copies preserve their own positions and retain the snapshot after the original Map leaves scope.

The stage0 pin reserves the public method name as a builtin.
The source implementation uses __iterator, which both checkers expose as iter.
Typed call annotations retain the implementation name for normal source method calls.
This keeps the standard library compatible with the pin without a second runtime implementation.

The old MapIter type, methods, interpreter values, runtime helpers, and semantic operations are removed.
Its retired semantic instruction numbers remain invalid slots.
The registry test walks the complete positional table, and checks those slots.
The cursor-specific escape and Cell rules are also removed.
An owned snapshot can leave the function that created its Map.

The self-hosted direct Map loop already reads parallel key and value snapshots.
It retains that path. The Go oracle now lowers its Map loop through the same two columns.
Both loop forms preserve the step on continue and the loop label.
The profile in the validation directory measures the separate direct loop path.

## Generic keys

Issue #13056 exposed a tuple type decoded into a nested generic clone name as source text.
The key decoder now encodes those arguments before it joins the clone name.
Array arguments also retain their shape in the key.
Raw argument boundaries remain separate until the compiler forms symbol names, and the decoder restores array and view types.
Tests distinguish a generic record with an array argument from an array of generic records.
They also compile tuple iterators through nested adapters on all three targets.

## Checker calls

Issue #13057 exposed missing argument checks for source methods declared on Map.
The Map receiver branch now applies the declared method signature to the written arguments.
It checks argument counts, argument types, and method type arguments.
A constructor without key or value types cannot satisfy a generic Map receiver.
This produces the same coded error as the Go checker.

The iterator checker cases now use the import-aware checker driver.
Their method declarations and Iterator trait come from the real standard library modules.
A parser-only driver cannot supply imported method signatures.
Each case also runs with main inside a lambda and compares the two checkers.

## Validation

The source commit is `2669c6a5d740583e24d17ffbf9abd865d5b54476`.
The three-stage build reaches a byte-identical fixpoint.
All 11 driver sizes pass the unchanged strict baseline gate.
The targeted runtime and checker suite passes, and stage 3 passes 80 fixture checks with the fresh Go oracle.
Allocation counts equal free counts in native and core WebAssembly programs under the sanitizer.

See [the complete validation records](../benchmarks/map-iterator-2026-10-23/README.md).

## Current integration

The current check includes the JSON, codec, system, CSV, record, capture and OS text corrections.
All 4965 selected compiler cases pass without failures or skips.
The Go loader also removes the retired cursor helper exemption.
The final Go sweep and fresh-oracle matrix check that correction.
The matrix passes 88 commands and balances all 24 runtime allocation counts.
The separate direct-loop allocation count stays unchanged at 60000 for 10000 loops on all three execution targets.

Exact source scopes, current bootstrap identities and output logs are in
[the current integration records](../benchmarks/map-iterator-2026-10-23/current-integration/README.md).
