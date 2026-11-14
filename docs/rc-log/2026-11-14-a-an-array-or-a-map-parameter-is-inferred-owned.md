# 2026-11-14 — an array or a map parameter is inferred owned

All targets. `ssasem.owned_shape`, read by `semsource.inferred_mode` and
`semsink.lent_to`. Fixes #15810.

## The shape

```fern
@noinline
function decode(bytes: u8[]): string {
    return unsafe { string_from_bytes_unchecked(bytes) };
}
```

The escape analysis found `bytes` consumed. `owned_shape` accepted only a
record or a variant, so `bytes` stayed borrowed. The callee retained it
before the sink, and the sink found a shared buffer and copied it. A map
parameter that a callee inserts into and returns had the same problem:
`__map_cow_inplace` copied the map on each call.

## Why arrays were left out

Commit cd1d717c83 (2026-09-21) excluded strings and arrays together. Its
measurement found a regression only on string parameters: `ascii_scan`
1.049x and `utf8_ingest_validated` 1.025x. Maps were excluded with cells,
handles, closure environments and views. Each of the 43 builtins that take
an array now has an ownership contract. Thus the analysis finds a parameter
consumed only where a callee keeps it.

## What changed

`owned_shape` accepts an owned array of any element type and a map. A view
array stays excluded. `semsink.child_spent_between_for` used `owned_shape`
to find a record or variant field. It now uses `box_shape`, so its rule does
not change.

Strings stay borrowed. A concatenation lends its operands, so the analysis
does not find a string parameter consumed by `s + t`. We put strings in
`owned_shape` as a test. A loop of 200 concatenations through a parameter
still made 200 allocations, and `http_hello` got 0.33% slower in Callgrind
instructions.

## Measured

Allocations under `FERN_LEAKCHECK`, the same on x86-64, arm64 under qemu and
wasm32-wasi:

| Case | main | this change |
| --- | ---: | ---: |
| 50 sinks of a fresh 4,096-byte `u8[]` | 100 | 50 |
| a sink on one branch, 20 calls | 30 | 20 |
| 400 inserts through a map parameter | 804 | 4 |

On x86-64, the compiler that compiles `checker.fern` (`selfhost-alloc-bench`)
makes 46,974,104 allocations on main and 46,691,972 with this change (-0.60%).

Four sinks of a 200 MB `u8[]` through a parameter take 0.60 s and 391 MB
peak RSS on main. With this change, they take 0.19 s to 0.24 s and 195 MB.

The compiler reproduces itself (stage2 == stage3). Stage 2 is 17,583,184
bytes on main and 17,366,416 bytes with this change (-1.23%). Compile time
and peak RSS of the self-compile do not change within the noise of the
machine: 61 s to 66 s and 2.68 GB for both.

Bench corpus, x86-64 Callgrind instructions: no row gets slower.
`utf8_ingest_unchecked` -0.67%, `utf8_ingest_validated` -0.28%, and
`deflate_decode`, `deflate_encode` and `ordmap_insert` less than -0.02%.
The emitted size of `ordmap_insert` falls 4.1% to 6.4% on the three
targets.

## Traps

- `map_string` gives a different Callgrind count on each run of the same
  binary, by up to 0.4%. Do not read a move in it as an effect.
- This machine ran other agents' builds during the measurements. Two
  stage-2 runs of one compiler differed by 15 s. Use interleaved runs of
  the user time, not one distcheck line, to compare compile time.
