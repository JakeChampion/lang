# Raw-storage costs on current main

This repair preserves author head `a30cec0fa5` and main `5035c7e73b`.
Main includes the kernel comparison changes and the JNI shared-object work.
The earlier records keep their original source and producer scopes.

Only nonempty mutable payloads leave the constant pool when a raw path reaches them.
Immutable containers and empty arrays retain their constant storage.
A raw cell read demands every store with the same cell type.
The worklist revisits those stores before convergence.
Unknown indirect calls retain the conservative rule.

Flat type queries avoid a temporary worklist.
Recursive schemas retain the cycle check.
Constant placement reuses the final reach for the same input graphs.
The plan cache compares complete return and cell demands, plus exact parameter flags.
Empty name indexes need no bucket array.

PIE keeps its virtual constant gap out of the file.
A mapped zero-fill range permits startup protection without an unmapped hole.
Constant headers and relocation markers use eight-byte alignment.
The permission controls cover both Linux targets and QEMU with 64 KiB pages.
Writes into code or constants and execution from data still fault.

The compiler is `25244cc353c2eac328522e2736fbf00bee1c62ac21b95dd4c1631887df83a251`.
Stages 2, 3 and 4 match.
The later proof-parser corrections produce that exact compiler again.
Their records preserve the preceding stage inputs and the new proof inputs separately.
The proof parsers accept data byte alignment and still reject alignment inside a function body.

| Compiler cost | Prior pin | Observed |
|---|---:|---:|
| Allocations and frees | 36037176 | 36355630 |
| Append crossings | 343679 | 347070 |
| Append bytes | 46426872 | 46205120 |

All three values pass the unchanged one-percent tolerance.
The append replay uses the pinned stage-0 compiler to build stage 1.
The allocation replay uses the qualified compiler to build the instrumented compiler.
QEMU supplies target execution for these counts, with no native timing claim.
These producer scopes differ from the earlier self-produced cliff diagnostics.

All 39 benchmark outputs match the qualified kernel producer.
The 78 ARM metrics, 39 x86 static metrics and 117 advisory size metrics pass.
The Wasm ordered-map size remains 1.21 percent above its old pin under the advisory rule.
All 144 static profile metrics pass after 23 lower image limits.
No limit or tolerance increases.
Native x86 dynamic CI remains required.

All 11 network regression roots pass.
The 1205-case leak census retains its existing five leaky fixtures and 18 unpaired allocations.
The unsafe array-box control initializes reserved elements before it reads them.
Its expected output stays unchanged.
The source checks and lint pass.

All nine retained-driver controls pass on ARM64, x86-64 and Wasm.
Each records balanced nonzero counts and zero live bytes.
