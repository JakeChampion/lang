# Tuple array element inference

Refs #13695. Parent #13694 keeps its application files unchanged until this repair merges.

The Go checker accepts `[([], 0), ([1], 1)]` and its interpreter returns the expected result.
The self-host checker also accepts this source before the repair.
All three compiled targets then refuse typed lowering because the array binding retains an unknown tuple component.
The reverse-order, direct nested-array, and explicit-type controls succeed before the repair.

The array checker now joins partial tuple components through the existing value-block join.
Each peer must settle to the shared type with the existing view and nominal checks.
Invalid joins retain an unresolved receiver for subsequent method diagnostics.
The tuple array diagnostic also checks nominal arguments instead of only nominal names.
The Go checker and both application files remain unchanged.

The focused regression checks twelve positive programs across native, ARM64, and Wasm targets.
Each program also passes the Go checker and interpreter.
Eight negative programs require the exact Go diagnostic-code sequence on every compiled target.
The final focused run passes 82 test nodes with no failures or skips.
All compiled positive cases require a nonzero balanced heap census with zero live bytes.

The first test draft omitted an equality derivation on its enum control.
The Go checker correctly rejected that draft with E041.
The corrected control uses the canonical equality derivation.
The preserved first production candidate passes the positive cases but misses three follow-on diagnostic sequences.
The final candidate preserves those strict assertions and passes them.

Broader validation and source-bound evidence follow before merge.
