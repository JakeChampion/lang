# The checked module is annotated once

This is the first slice of "facts in side tables" in #11534: the checker types each function once, and the build reads that work.

Before this change, the build typed each function three times:

- The checker prepared and qualified its own copy of the module, and checked it.
- The ownership pass of the checker, `own_diags`, annotated each function that returns a pointer type and calls a method.
- After the build gate, the driver prepared the module again and annotated every function with `checker.annotate_module`.

`checked_module` now annotates its prepared module once and keeps it in `ModuleTypes.typed`.
`own_diags` reads the annotated bodies from it.
It annotates a function only when the typed module does not hold it, such as a clone that the second check pass instantiates.
The build gate returns the typed module with its diagnostics, as `checker.GateResult`.
The driver lowers that module, and its own preparation and annotation are deleted.

Two experiments showed that the move keeps the output.
In the first, the driver annotated the checker's prepared module directly after the gate, and it annotated the module again in the old place.
In the second, the driver annotated the checker's prepared module after the gate, and the later annotation was removed.
Each time, the subject compiled to the same executable and assembly.
So the passes between the gate and the old annotation keep the annotations valid.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,377,920,317 |
| this change | 28,695,264,166 (−2.32%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
