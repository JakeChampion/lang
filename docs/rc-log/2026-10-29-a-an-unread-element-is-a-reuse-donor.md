# 2026-10-29 — an element that nothing reads is a reuse donor

`ssaunits.reuse_pairs`. Fixes #13334.

## The shape

```fern
function spanned(r: P, lo: i64, hi: i64): P {
  return P { ...r, low: lo, high: hi };
}

function (h: H) full(x: i64): H {
  return H { ...h, ps: h.ps.with(0, spanned(h.ps[0], x - 1, x)) };
}
```

After the splice, `full` reads `v6 = array_get v2 0` and builds
`record_new v9 v1` with `v6` as its update base. The update writes every
field, so nothing reads `v6`. `payload_takes` takes `v6` out of its slot,
because the `with` writes the same slot. `v6` then dies at the step that
defines it.

`reuse_pairs` makes a dying value a pending donor for a later construction of
the same slot count. It skipped a value that is the result of its own step
(`d != ins.result`). That rule is for `selfs`, where a construction must not
donate to itself. For a pending donor it removed exactly the value that
nothing reads. So the element was released, and the record took a fresh box.

An update that keeps one field reads it with `record_get`. The element then
dies at that read, which is a later step, and it is a donor.

## What changed

The pending donor test no longer excludes the step's result. A token for such
a value is taken at its own step and spent at the construction.

## Measured

| Shape, 1,000 calls | Before (x86-64) | After (x86-64, arm64, wasm32-wasi) |
|---|--:|--:|
| `partial`: `P { ...r, high: x }` | 3 | 3 |
| `full`: `P { ...r, low: lo, high: hi }` | 1,000 | 0 |

The three allocations of `partial` are in its first calls, before the array
and the element are the frame's alone. Both shapes are balanced under
`-sanitize`.

`TestSelfHostFullRecordUpdateReusesItsBase` calls each shape once first, then
pins 0 allocations for 1,000 more calls of each, on the three targets.

## Traps

Counting the update base as a use in `ssasem.uses_of` looked like the direct
fix. It made both shapes worse: 2 allocations per call. The take of the
element then saw a later use at the construction, decided the element was
needed past its slot, and copied it.
