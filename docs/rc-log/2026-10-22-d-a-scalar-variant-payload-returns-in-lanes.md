# 2026-10-22 — a scalar variant payload returns in the lanes

`semscalarreturn.lanes_of`, `variant_in_lanes`, `set_lanes` and
`from_lanes`. `sempair.captured_lanes`, `arm_lanes` and `dispatched`.
Refs #12887.

## The shape

```fern
@noinline function find(xs: i64[], want: i64): Option[i64] {
  for x in xs {
    if (x == want) {
      return Some(x * 2);
    }
  }
  return None;
}
```

`sempair` returns a variant as its position and one payload word. A payload
that does not fit the word took a box. An `i64` or `f64` does not fit, because
the word is a pointer wide on wasm32. A tuple fits only as a pointer to a box.
So `Some(x * 2)` built a box on every call, and so did `Some((i, xs[i]))` and
`Ok((value, at))`.

`semscalarreturn` already returns a scalar record in three more 64-bit lanes:
`rdx`, `rcx` and `r11` on x86-64, `x1` to `x3` on arm64, and three globals on
wasm. Its lanes carry no ownership unit.

## The rule

A variant takes the lanes when every payload is a scalar or a tuple of up to
three scalars. At least one of them must also need a box in the word. That is
`variant_in_lanes`. Its returns set each payload's parts in lanes 0 and up,
and return the position as a lane word. Each call captures the word and the
lanes, and rebuilds the variant from them as a branch on the position. Split
then reads the caller's match off the rebuilt constructions, as it does for
the word form.

A variant whose payloads all fit the word keeps the word form, which needs no
change. A variant with a reference payload as well, like
`Result[(i64, i32), QpackError]`, takes neither form yet.

The certifier reads a paired call's ABI from the declared result type. For a
variant in the lanes, `physicalbody.pair_second` now says that the word after
the call holds no unit. Before, it marked the tuple payload as a reference,
and the certifier reported the first lane as a leak.

## Measured

x86-64, `TestSelfHostPairReturn`'s new `lanes` program, 12 rounds of seven
calls under `FERN_LEAKCHECK`:

| Build | Allocations in the loop |
|---|---:|
| stage0 | 60 |
| This change | 0 |

A probe calls an `Option[i64]` function and an `Option[(i32, i64)]`
function 1,000 times each. It allocated 1,750 boxes and now allocates none. The
three backends and the interpreter agree on the program's value.

The H3 serve loop does not move: 200.40 a serial request and 111.70 a
multiplexed one (`TestSelfHostServeAllocsCensusH3`). Its tuple payloads come
back through `qpack.__sec_int`, whose error holds a string. No perf row moves,
and the drivers move by 0.3% or less.

## Next

Return a variant with both a reference payload and scalar payloads, so that
`qpack.__sec_int` builds no box. The pointer word and the lanes overlap on
x86-64 and arm64 but not on wasm. The caller must capture both, and must store
the lanes before `r11` is reused.
