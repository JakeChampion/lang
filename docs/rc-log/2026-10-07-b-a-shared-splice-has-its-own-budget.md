# A shared splice has its own budget

2026-10-07: `seminline.max_shared_insts`. #11760, against the shared
splice of 2026-10-06-j.

## What the budget trades

A function called once and spliced leaves no original behind
(`ircore.cull_spliced_leaves`), so the program does not grow. A shared one,
spliced into its two callers, keeps its original wherever a caller keeps the
result whole, and every copy is code the lift, the analyses and the
allocator pay for on every compile. The box a copy saves is one allocation
per call at run time. Shared splices took the once budget of 400
instructions; they now take their own, 100.

## Measured

Each setting is main at 6314e4340 with `seminline.fern` changed. That
stage 2 is built by main's compiler, and it builds itself as stage 3. The
measured run is stage 3 compiling main's `compiler/checker.fern` for
x86-64-linux: Ir from callgrind, allocations from a `FERN_LEAKCHECK` build
of the same stage 3. Both are exact.

| shared budget | Ir | allocations | stage 3 bytes | serve loop, allocations per hello request |
|---|--:|--:|--:|--:|
| 400 (before) | 15,879,899,529 | 23,879,169 | 11,453,928 | 0 |
| 200 | 15,879,811,210 | 23,879,137 | 11,433,160 | |
| 150 | 15,505,492,664 | 23,544,868 | 11,196,176 | |
| 100 | 15,435,286,789 | 23,493,795 | 11,166,752 | 0 |
| 50 | 15,429,105,512 | 23,513,778 | 11,105,592 | 2 |
| off (`max_shared_callers` 1) | 15,287,353,720 | 23,453,181 | 10,966,256 | 2 |

The two effects come apart with two more runs per setting:

- **Generated code:** the setting's stage 2 builds main's sources, giving a
  compiler that behaves as main and differs only in its own code. Against
  400 its allocations rise 0.38% at 100 (23,970,434) and 0.81% with the pass
  off (24,071,633). Its Ir rises 0.10% (15,895,108,597) and 0.16%
  (15,905,437,454).
- **Compile work:** the setting's stage 2 itself, built by main's compiler,
  so its code is main's and only the work the setting causes differs.
  Against 400 its Ir falls 2.89% at 100 (15,420,077,715) and 3.89% off
  (15,262,033,494). Its allocations fall 2.00% (23,402,530) and 2.58%
  (23,262,587), and `checker.fern`'s emitted asm falls 3.6% and 4.3%.

So the copies pay back in neither half: what they save in the compiler's own
run is a tenth of what lifting them costs.

## Why 100 and not off

The shared functions of the serve program (`TestSelfHostServeAllocsPerRequest`),
with their sizes at 400:

```
3 net__socket_addr            33 serve____serve_produce
3 platform__host_on           35 serve____serve_start
6 async__real_driver          44 http____chunked_trailers
6 http__not_found             46 http____chunk_size_line
6 http__ok                    53 serve____serve_park
8 serve____stop_none          75 http____request_head
27 serve____serve_send       118 serve____serve_read
```

At 50 the hello request allocates twice, as it does with the pass off;
`__serve_park` and `__request_head` are among the bodies dropped, for 0.04%
of Ir past what 100 saves. At 100 it stays at 0, though `__serve_read` is
dropped. The `checker.fern` build shares 30 functions, the largest 199
instructions, which is why 200 measures as 400.

## Witnessed

`TestSelfHostSharedConstructions` gains `wide`, `span`'s shape with a
178-instruction body. It stays a call from `apart_wide` and
`apart_wide_again`, and each of their rounds builds the record, on all four
targets; main's compiler splices it. The budget-100 stage 3 rebuilds itself
byte for byte. The `utf8_ingest` rows of `.github/perf-baseline-selfhost.txt`
fall 4.9-7.0%, giving back #11745's growth, and the stage2 row of
`.github/selfhost-driver-sizes.txt` is 11166752.
