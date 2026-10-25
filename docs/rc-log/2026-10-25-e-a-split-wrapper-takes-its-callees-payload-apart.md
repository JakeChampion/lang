# 2026-10-25 — a split wrapper takes its callee's payload apart

`sempair.payloads_whole`, `seeded` and `returned_constructions`. Found on
the HTTP/3 hello path.

## The shape

`std/http/qpack.__sec_str`, reduced:

```fern
function __sec_str(buf: [u8], at: i32, prefix: i32, room: i64): Result[(string, i32), QpackError] {
  match (__str(buf, at, prefix, room)) {
    Ok(r) => {
      return Ok(r);
    },
    ...
  }
}
```

`__str` returns its `(string, i32)` in the three-word form only while no
caller keeps the payload whole. `seeded` marked every argument of a
construction whole, so `Ok(r)` kept `r`, and `__str` built its pair on
every call. But `__sec_str` is split itself: its return reads the two
parts out of `r` (`parts_of`) and builds nothing either.

## What changed

A variant a split function returns no longer keeps its payload whole
(`returned_constructions`, passed to `seeded` from `payloads_whole`). The
rule holds only while the wrapper stays split; one whose own caller keeps
the result whole returns it in one word and keeps the inner payload whole
as before.

## Measured

| Row | Before | After |
|---|---:|---:|
| `TestSelfHostPairReturn`'s `forward` program, 20 calls through the wrapper, x86-64 | 12 | 0 |
| `TestSelfHostServeAllocsPerRequestH3`, on main with #13276 | 81.88 | 80.88 |
| `TestSelfHostServeAllocsPerRequestH3Mux` | 47.63 | 46.61 |

## Next

`qpack.__sec_int` returns `Result[(i64, i32), QpackError]`. An `i64` part
does not fit the pair word, which is 32 bits on wasm, and the error holds
a string, so the lanes form does not take it either. That is five boxes a
request on the hello, with `__required`'s `Result[i64, QpackError]` and
`__prefix`'s triple three more.
