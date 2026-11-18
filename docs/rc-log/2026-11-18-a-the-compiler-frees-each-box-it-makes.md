# The compiler frees each box it makes

2026-11-18. x86-64. No code changes. This entry records a leak census of
the compiler at main `86e1c1d1fb`.

## Measured

The compiler was built with `FERN_LEAKCHECK=1` and the embedded assets
(`-embed build/embed`). Then it compiled three programs for x86-64:

| program | allocations | frees | live bytes |
|---|---:|---:|---:|
| `bench/perceus/fern/run_rbtree.fern` | 3,099,471 | 3,099,471 | 0 |
| `compiler/drivers/ssacreditreuse_run.fern` | 167,921,803 | 167,921,803 | 0 |
| `compiler/fern_main.fern` (the compiler) | 429,660,001 | 429,660,001 | 0 |

The compile of the compiler took 87 seconds. Each census is balanced, so
the compiler leaks nothing on these inputs.

## To repeat

```
./scripts/fern-embed-stage build/embed
FERN_LEAKCHECK=1 bin/fern-selfhost -target x86-64-linux -embed build/embed \
  -o fern-lk compiler/fern_main.fern internal/stdlib
./fern-lk -target x86-64-linux -embed build/embed -o self.bin \
  compiler/fern_main.fern internal/stdlib
```

The census line `leakcheck: allocs=... frees=... live_bytes=...` is on
standard error.
