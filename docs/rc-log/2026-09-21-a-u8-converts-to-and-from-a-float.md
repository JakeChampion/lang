# 2026-09-21 — a u8 converts to and from a float

`examples/cli/bc` is 125 declarations and produced none of them, on one
refusal: `cast contract: u8 as f64`.

`ssasem.cast_admits` excluded `u8` from both float directions by name:

```fern
if (is_float(to)) { return is_integer(from) && !is_u8(from); }
if (is_float(from)) { return is_integer(to) && !is_u8(to); }
```

Native compiles and runs the same program, so the exclusion was not a language
rule. It read like a missing feature.

## What the exclusion was actually covering

Only one of the two directions was missing anything.

INTO a float was already right: `ssarc.into_float` takes `bits = 32` and
`is_unsigned_int(u8) = true`, so it emits `op_int_to_f64(32, true)`, which is
the correct conversion for a slot holding a zero-extended byte. That side
needed the guard removed and nothing else.

OUT OF a float was not. `ssarc.cast` returned `emit(r, from_f64(to))` and
stopped, and `from_f64` falls through to `op_f64_to_i32()` for a u8
destination — no mask. Ten lines below it, the wide-to-narrow arm does apply
one: `if (!ssasem.is_u8(to)) { return r; } return emit(r, ir.op_int_cast("u8"))`.
So removing the guard on its own would have turned a refusal into a
miscompile: `300.7 as u8` would keep 300 where every other lowering answers 44.

`out_of_float` is that pair — the truncation, then the u8 mask — and the guard
comes off both directions.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle.

| program | before | after | typed held |
|---|---|---|---|
| every byte round-tripped through f64, plus the truncations | 0 of 1 | 1 of 1 | 0 B |
| `examples/cli/bc` | 0 of 125 | 125 of 125 | — |

`bc` answers identically on the typed path, the AST leg and native for
`2+3*4`, `10/4`, `2^10`, `(1+2)*(3+4)`, `7%3` and `1.5*2`. The round-trip
program answers 128 on the typed path, the AST leg, native, arm64 under qemu
and the sanitize leg.

Corpus census (865 seeds), both legs run here with the same script:

| | before | after |
|---|---|---|
| programs produced whole | 831 | 832 |
| declarations produced | 84,758 of 87,251 | 84,883 of 87,251 |

`examples/cli/bc` is the only file that moves. The baseline for it was retaken
on this branch's own base: a census carried across a reset onto a newer main
folds that main's movement into the delta, which is how a first attempt at
these numbers showed `ndarray_test` gaining nine declarations from #9906.

## Traps

- **The guard was load-bearing in one direction.** Reading `cast_admits`
  suggests two symmetric exclusions of the same kind. They were not: one
  guarded nothing and one guarded a missing mask. Taking both off together,
  without the mask, is a silent wrong answer rather than a refusal — the shape
  the typed path exists to avoid.
- **A negative source diverges from native, and did before this.** `-1.0 as u8`
  is 255 on both self-host lowerings and 0 on native, which saturates. The
  positive cases agree everywhere, in range and out. This change makes the
  typed path match the AST leg on every case, so it neither introduces the
  divergence nor widens it, and the test pins only the rows all three legs
  agree on so it survives the fix. Filed as #9912.
