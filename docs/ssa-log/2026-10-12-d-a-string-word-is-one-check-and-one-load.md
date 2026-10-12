# 2026-10-12 — a string's word is one check and one load

`ssabounds.grouped_reads`, `ssabounds.word_loads`, the check-only
`str_index`. Refs #12105.

## What changed

- **Strings join the grouped reads.** `grouped_reads` and `word_loads`
  matched only `array_get`, so a word built from a string's bytes,
  `s[i] as u32 | s[i + 1] as u32 << 8 | …`, kept a bounds check, a reload of
  the string's data pointer and a byte load for every byte. Both passes now
  take `str_index` reads as well.
- **A string's check loads nothing.** A `str_index` with `imm`
  `ssasem.check_only()` lowers to `str_index` with `i32_imm` 2
  (`ir.check_only`): on x86-64 and arm64, the compare and the trap; on wasm,
  the compare and `unreachable`, pushing a zero. This is the array's
  check-only from 2026-10-10, for the string layouts.
- **The fused load reads from `__raw_data`.** A string's word is
  `__load_i32` / `__load_i64` at `__raw_data(s)` plus the index at pointer
  width, which covers a slice's view box as well as an owned string.

core/map's string hash, `__map_fnv_str_seeded`, reads every key four bytes at
a time. Its loop body on x86-64 went from 45 instructions (four checks, four
data-pointer reloads, four byte loads, three shifts and ors) to 18:

```
leaq 3(%r8), %r9 ; movslq %r9d, %r9 ; movq %r8, %r10 ; sarq $31, %r10
orq %r10, %r9 ; cmpq 8(%rax), %r9 ; jae __fern_oob_abort
movslq %r8d, %r10 ; movq 0(%rax), %r9 ; addq %r10, %r9 ; movslq (%r9), %r9
```

## Measured

x86-64, callgrind, against the same base (the `__raw_addr` retirement on
main at 5d1ea980).

| | base | this change | change |
|---|--:|--:|--:|
| `bench/map_string` | 82,500,988 | 66,093,136 | −19.9% |
| std/json, twitter.json, one parse | 37,153,849 | 35,855,457 | −3.5% |
| std/json, citm_catalog.json | 100,433,872 | 100,148,080 | −0.3% |
| std/json, canada.json | 191,952,608 | 191,952,514 | 0.0% |

The `map_*` and `utf8_ingest_*` `.emit` rows fall by 0.1–0.5% on all three
targets. `ascii_scan` and `http_hello` grow by 4 to 12 emitted lines on
x86-64 and wasm: a run of two reads now pays for one probe index, five
instructions, in place of two compare-and-branch pairs. `ascii_scan.ir` does
not move.
