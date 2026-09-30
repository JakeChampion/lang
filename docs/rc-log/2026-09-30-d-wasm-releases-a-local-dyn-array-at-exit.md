# 2026-09-30 — wasm releases a function-local dyn array at exit (#10551)

Native (`internal/ir`: the exit-sweep dyn arm in `rc_insert.go`, and
`decValueOnStack`).

Both arms were gated on `ptrW == 8`. The recorded reason was that wasm's inline
two-word elements double-drop when one is bound out. That is no longer so:
`emitDynRetain` retains `data` on the inline representation, and
`docs/DYN-TRAITS.md` §7.8 already listed the array-element kind as reclaiming
on wasm. Lifting only the exit-sweep gate made it worse (frees 0), because
`decValueOnStack`'s twin gate sent the value to a flat `__fern_rc_dec` that
never frees. Both had to go.

## Measured (wasm, allocs / frees)

| shape | before | after |
|---|---|---|
| issue program | 2 / 1 | 2 / 2 |
| iterate + `keep(xs[0])` + bind + returned array | 5 / 2 | 5 / 5 |
| elements escaping the function, heap strings | 33 / 22 | 33 / 33 |
| heap growth, 50 → 5,000 calls | 3,232 → 320,032 B | bounded |

`TestWASMDynShapeArrayExitBounded` pins the bound and a zero
`__rc_underflow_count()`. x86-64 was already balanced (48 / 48) and still is.
