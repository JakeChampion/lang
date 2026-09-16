# Measured 2026-09-16: every other i32 constant divisor multiplies by its reciprocal

**Every other i32 constant divisor, the same day.** The power-of-two
lowering left `n / 100` and `n % 100` in `to_string`'s digit loop on a real
`div`, two per pair of digits. The shared emitter now lowers an i32
division or remainder by any constant that is neither 0, ±1 nor a power of
two through the reciprocals in `internal/ir/magic.go`, as the flat x86-64
backend does, in the abstract ops both renderers had: widen, multiply by
the magic, take the high half, then the reciprocal's fixups
(`emitMagicDivRem`). `coreutils/sort.fern` keeps 6 of the 23 divisions it
had before #9432; the `to_string` loop above moves 0.050 s → 0.045 s on its
own, the rest of its gap being the copy startup recorded next.
