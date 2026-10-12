# A shift takes its dying operand's home (#12214)

`shares_operand_home` listed the two-operand ALU ops and the compares, so a
binary whose left operand died at it could put its result in that operand's
register or slot. The shifts were missing: `x << 8` with `x` dead after it
copied `x` into a fresh register and shifted there. `shl`, `shr_s` and
`shr_u` now share. Every emission path reads a shift's operands before it
writes the result: x86-64 loads the left operand into the destination and
shifts in place for a constant count, and stages both in its scratch for a
register count; arm64's `lsl`/`asr`/`lsr` read their sources first.

`(a & 255) << 16 | (b & 255) << 8 | c` went from ten x86-64 instructions to
seven. `scripts/perf-bench`, x86-64, against main at 168acf0:

| row | change |
|---|---|
| `map_probe_chain.ir` | -2.0% |
| `map_int.ir` | -2.0% |
| `utf8_ingest_validated.ir` | -1.5% |
| `pmap_insert.ir` | -1.1% |
| `enum_match.ir` | -0.8% |

No row rose. Pinned by `TestSelfHostOptimisationShapes/shift_in_operand_home`.
