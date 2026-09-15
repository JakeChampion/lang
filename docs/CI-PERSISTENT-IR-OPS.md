# Persistent instruction accumulation

Lowering keeps snapshots of its state. A shared flat instruction array makes
an append copy every earlier instruction. Tracing a self-built compiler while
it lowers `asm_ir.emit_ir_runtime` attributed 3,171,509,704 copied bytes to
`LowerState.emit` alone.

`ir.OpBuffer` stores full chunks in an immutable reverse chain and a tail of
at most 32 instructions. Appending explicitly copies the small tail; it never
changes a previously published tail. Flattening is iterative and preserves
instruction order. Existing optimizers, results, and backends still receive
ordinary `Op[]` arrays.

The explicit copy matters: an initial field-append prototype passed output
comparisons but failed retained-snapshot tests. That version was rejected.
The accepted implementation is tested with buffers held in another container,
divergent appends, repeated flattening, empty/chunk-boundary lengths, and
instruction/string payloads. The existing generic persistent vector was also
evaluated, but current per-module emission cannot specialize that imported
generic library. No eligibility or ownership proof was weakened.

## Controlled measurement

Native Linux ARM64, four CPUs, 16 GiB limit, baseline source revision
`78a457c36beeb0b5d01d5a1ecd59b27e8d2c5217`, 100-function emission budget,
unchanged eight-unit batch [8:16]. Each generation used the order baseline,
candidate, candidate, baseline. Every output unit matched byte for byte.
Both generations were rebuilt after the semantic field-append changes merged;
the candidate includes the instruction-buffer change on that same baseline.

| Compiler | Baseline wall seconds | Candidate wall seconds | Baseline peak RSS | Candidate peak RSS |
| --- | --- | --- | --- | --- |
| Go-built | 6.894, 6.726 | 6.163, 6.065 | 3.365 GB | 3.358 GB |
| Self-built | 12.108, 12.528 | 5.709, 5.669 | 12.496 GB | 3.044 GB |

GB here means decimal bytes. Mean wall time is 6.810 -> 6.114 seconds for the
Go-built compiler and 12.318 -> 5.689 seconds for the self-built compiler.
These are local batch measurements, not whole-CI or x86 performance claims.
Each measurement followed a passing single-module pilot.

## Native x86 self-built compiler

[Run 35029998597](https://github.com/JakeChampion/lang/actions/runs/35029998597)
compared the same baseline and buffer change on one four-CPU Linux x86 host,
after a passing pilot on the same experiment revision. The self-built compiler
compiled identical baseline sources in the same eight-unit window [8:16].

| Mode | Wall seconds | Peak RSS |
| --- | ---: | ---: |
| Baseline 1 | 13.485 | 11.791 GB |
| Candidate 1 | 10.029 | 2.954 GB |
| Candidate 2 | 9.991 | 2.954 GB |
| Baseline 2 | 13.635 | 11.791 GB |

Mean time is 13.560 -> 10.010 seconds. All eight output units match in every
trial. Separately, all 75 units of the candidate reproduce between its Go-built
and self-built generations; the largest of those batches peaks at 2.959 GB.
The artifact `native-buffer-full-gen1-1` records build identities, environment,
per-process measurements, exact output hashes and completed verification.
The Go-built generation still needs its separate native x86 comparison.

## Correctness and size

- All 75 units of the changed compiler reproduce exactly between the Go-built
  and self-built generations, using the normal eight-unit emission route.
- Snapshot regressions pass through x86, ARM64, and Wasm emission, including a
  zero-reference-count-underflow assertion. QEMU execution is correctness-only.
- The semantic-source ownership regression also passes on ARM64, x86, Wasm,
  and the x86 sanitizer path. Neither test skips a target in this validation.
- The native ARM64 Go-built binary shrinks from 11,058,273 to 10,992,749 bytes.
  The self-built binary grows from 10,637,176 to 10,702,064 bytes. ELF section
  comparison attributes 6,569 bytes to added loadable contents; its data segment
  crosses a 64 KiB alignment boundary, explaining most of the file-size growth.
  BSS is unchanged. No size baseline or memory reservation is changed.

Full repository checks, a controlled parallel-batch comparison, and live CI
validation remain required before adopting new scheduling or memory budgets.
