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

Native Linux ARM64, four CPUs, 16 GiB limit, original source revision
`819c5ee8d86ec058630342fc025bfef9479273d8`, 100-function emission budget,
unchanged eight-unit batch [8:16]. Each generation used the order baseline,
candidate, candidate, baseline. Every output unit matched byte for byte.

| Compiler | Baseline wall seconds | Candidate wall seconds | Baseline peak RSS | Candidate peak RSS |
| --- | --- | --- | --- | --- |
| Go-built | 7.019, 6.714 | 6.151, 6.024 | 3.363 GB | 3.354 GB |
| Self-built | 12.326, 12.137 | 5.768, 5.569 | 12.496 GB | 3.042 GB |

GB here means decimal bytes. Mean wall time is 6.867 -> 6.087 seconds for the
Go-built compiler and 12.232 -> 5.669 seconds for the self-built compiler.
These are local batch measurements, not whole-CI or x86 performance claims.
Each measurement followed a passing single-module pilot.

## Correctness and size

- All 75 units of the changed compiler reproduce exactly between the Go-built
  and self-built generations, using the normal eight-unit emission route.
- Snapshot regressions pass through x86, ARM64, and Wasm emission, including a
  zero-reference-count-underflow assertion. QEMU execution is correctness-only.
- The native ARM64 Go-built binary shrinks from 11,058,273 to 10,992,749 bytes.
  The self-built binary grows from 10,636,720 to 10,701,608 bytes. ELF section
  comparison attributes 6,569 bytes to added loadable contents; its data segment
  crosses a 64 KiB alignment boundary, explaining most of the file-size growth.
  BSS is unchanged. No size baseline or memory reservation is changed.

Full repository checks, native x86 measurements, and live CI validation remain
required before adopting new scheduling or memory budgets.
