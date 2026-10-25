# 2026-10-25 — a 64-bit payload part returns in its slot's lane

`sempair.fits_part`, `in_lane`, `captured`, `arm_return` and
`arm_payload`; `semscalarreturn.lane_set` / `lane_as`;
`ssasem.pair_capture_imm`'s lane bits; `ssarc.capture_pair_words`;
`rccertify.word_mask`. Found on the HTTP/3 hello path.

## The shape

```fern
function __sec_int(buf: [u8], at: i32, prefix: i32): Result[(i64, i32), QpackError] { ... }
function (d: Decoder) __required(encoded: i64): Result[i64, QpackError] { ... }
```

A pair word is pointer wide, 32 bits on wasm, so an `i64` fits neither a
pair word nor, beside the error's string, the scalar lanes. Each `Ok` built
a box: five `(i64, i32)` tuples and one `i64` a request on the hello.

## What changed

A split part, or a lone payload, that is a 64-bit integer goes in the
64-bit lane of its pair slot. That lane is the slot's own register on
x86-64 and arm64 (`rdx`, `rcx`; `x1`, `x2`) and `$__fern_scalar_word<slot>`
on wasm, so the variant's other payloads keep the pair words beside it.

- The callee sets the part with `__scalar_bits` and `__scalar_set:<slot>`,
  the lanes form's own ops.
- The call's capture immediate carries a bit per slot whose lane some
  variant uses. `capture_pair_words` stores those lanes after the pair
  words, and the caller reads them through `__pair_lane` / `__pair_lane1`
  and `__scalar_as`.
- `rccertify.word_mask` gives a lane (`word_slot` -1) no unit. Before, a
  lane after a paired call took the pair word's mask, and the certifier
  reported the `Ok` arm's lane as a leak.

## Measured

| Row | Before | After |
|---|---:|---:|
| `TestSelfHostPairReturn`'s `wide` program, 20 rounds of three calls, x86-64 | 48 | 0 |
| `qpack.Decoder.decode` of a four-field request section | 14 | 7 |
| `TestSelfHostServeAllocsPerRequestH3`, on main with #13276 | 80.88 | 73.88 |
| `TestSelfHostServeAllocsPerRequestH3Mux` | 46.61 | 39.56 |

The `wide` program agrees on x86-64, arm64 and wasm, and frees every
block.

## Next

`qpack.Decoder.__prefix` returns `Result[(i64, i64, i32), QpackError]`,
three parts: two boxes a request.
