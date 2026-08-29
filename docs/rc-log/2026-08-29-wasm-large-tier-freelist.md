# The wasm allocator stopped reclaiming at 512 KiB; the natives had not

`fl_cells` is 65536 size classes of 8 bytes, so the wasm small tier covers
blocks up to 512 KiB. Past that the allocator bumped and never recycled — the
emitted comment said so outright: "Blocks larger than `fl_cells*8` bytes are
bump-only (never recycled)."

Both native backends grew a large tier for exactly this (#3425):
`__fern_large_freelist`, linear 512-KiB classes, `class = round_up(bytes,
512 KiB) >> 19`, with the alloc side bumping AT the class capacity so every
block in a class has one size and any freed block satisfies any same-class
request. wasm never got the twin.

## What it cost, measured

`__heap_bump_bytes` is the metric that settles it: the cursor moves on a fresh
bump and never on a freelist reuse. A loop building a 640 KB `i32[]` by append
and dropping it each iteration, run at two iteration counts:

| iterations | before | after |
|---|---|---|
| 2 | 3584 KiB | 4608 KiB |
| 8 | **12800 KiB** | 4608 KiB |

Before, six extra iterations cost 9216 KiB — 1536 KiB gone per iteration, for
the life of the process. After, the cursor does not move at all: every block
comes back.

The 1024 KiB the "after" column pays at 2 iterations is the rounding to class
capacity, and it is the whole price. It is repaid by iteration 3.

## Shape of the port

Three sites gate on `cls < fl_cells`, and each grew an `else`:

- `$__fern_alloc` — pop from the large table, and on a miss bump at the class
  capacity rather than the request (setting `$n` to the capacity does both).
- `$__fern_arr_dec` (rc reaching 0) and `$__fern_alloc_reuse` (donor mismatch)
  — push through one shared `$__fern_large_push`, the wasm twin of asm_ir's
  helper of the same name, so the tier has a single choke point the way the
  natives do.

Over 1 GiB stays bump-only at the exact size, matching the natives.

The head array is 2049 cells wide (class 1..2048 covers 512 KiB..1 GiB) and
sits between the small table and the heap. Everything downstream of
`fl_base_for_strs` is derived, so `heap_base` — which is also the rc helpers'
low-address guard — shifted by 8196 bytes with no site to update by hand.

## Guard

`TestSelfHostWasmIRLargeFreelist` compares the bump at 2 and 8 iterations
rather than checking one figure against a constant: the absolute number depends
on how the growth path sizes its intermediates, but bump-only costs one block
per iteration whatever that is. Verified to fail without the change.
