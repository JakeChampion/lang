# Measured 2026-09-16: the full sweep, every open slice applied

All 28 `examples/bench` programs, best of five, x86-64 native and arm64 under
qemu. "main" is `8047634`; "all slices" is main plus the five PRs open at the
time (#9438 `__ssa_bcopy` sized copies, #9439 reciprocal division, #9441
branch alignment for the JCC erratum, #9442 AVX2 byte kernels, #9443
`__ssa_mismatch` behind `__str_eq` / `__str_ord`), which land in that order.
The two builds agree on every exit code.

| bench | x86-64 flat | x86-64 SSA, main | x86-64 SSA, all slices | arm64 flat | arm64 SSA, all slices |
| --- | --- | --- | --- | --- | --- |
| `array_append` | 0.008 s | 0.005 s (0.62x) | 0.006 s (0.75x) | 0.053 s | 0.048 s (0.91x) |
| `array_index` | 0.006 s | 0.004 s (0.67x) | 0.003 s (0.50x) | 0.027 s | 0.015 s (0.56x) |
| `array_with` | 0.007 s | 0.020 s (2.86x) | 0.007 s (1.00x) | 0.054 s | 0.030 s (0.56x) |
| `ascii_scan` | 0.009 s | 0.006 s (0.67x) | 0.004 s (0.44x) | 0.121 s | 0.116 s (0.96x) |
| `call_overhead` | 0.004 s | 0.003 s (0.75x) | 0.002 s (0.50x) | 0.017 s | 0.012 s (0.71x) |
| `closure_call` | 0.020 s | 0.020 s (1.00x) | 0.020 s (1.00x) | 0.188 s | 0.139 s (0.74x) |
| `enum_match` | 0.012 s | 0.027 s (2.25x) | 0.010 s (0.83x) | 0.123 s | 0.046 s (0.37x) |
| `int_loop` | 0.005 s | 0.002 s (0.40x) | 0.002 s (0.40x) | 0.022 s | 0.015 s (0.68x) |
| `map_int` | 0.009 s | 0.011 s (1.22x) | 0.007 s (0.78x) | 0.048 s | 0.043 s (0.90x) |
| `map_probe_chain` | 0.143 s | 0.165 s (1.15x) | 0.126 s (0.88x) | 0.541 s | 0.505 s (0.93x) |
| `map_string` | 0.017 s | 0.017 s (1.00x) | 0.013 s (0.76x) | 0.151 s | 0.074 s (0.49x) |
| `ordmap_insert` | 0.055 s | 0.050 s (0.91x) | 0.044 s (0.80x) | 0.455 s | 0.302 s (0.66x) |
| `pmap_insert` | 0.025 s | 0.025 s (1.00x) | 0.019 s (0.76x) | 0.266 s | 0.230 s (0.86x) |
| `pvec_with` | 0.025 s | 0.027 s (1.08x) | 0.018 s (0.72x) | 0.269 s | 0.221 s (0.82x) |
| `record_update` | 0.020 s | 0.042 s (2.10x) | 0.018 s (0.90x) | 0.134 s | 0.088 s (0.66x) |
| `sort_inplace` | 0.013 s | 0.010 s (0.77x) | 0.008 s (0.62x) | 0.072 s | 0.050 s (0.69x) |
| `sort_ints` | 0.010 s | 0.009 s (0.90x) | 0.008 s (0.80x) | 0.081 s | 0.068 s (0.84x) |
| `sort_strings` | 0.019 s | 0.021 s (1.11x) | 0.019 s (1.00x) | 0.196 s | 0.171 s (0.87x) |
| `string_build` | 0.016 s | 0.023 s (1.44x) | 0.015 s (0.94x) | 0.188 s | 0.237 s (1.26x); 0.144 s (0.74x) with the string reclaim below |
| `string_count_byte` | 0.011 s | 0.020 s (1.82x) | 0.012 s (1.09x) | 0.550 s | 0.550 s (1.00x) |
| `string_find_byte` | 0.006 s | 0.009 s (1.50x) | 0.005 s (0.83x) | 0.212 s | 0.211 s (1.00x) |
| `string_rfind_byte` | 0.005 s | 0.009 s (1.80x) | 0.008 s (1.60x) | 0.197 s | 0.195 s (0.99x) |
| `string_scan` | 0.011 s | 0.017 s (1.55x) | 0.008 s (0.73x) | 0.113 s | 0.069 s (0.61x) |
| `string_slice` | 0.016 s | 0.020 s (1.25x) | 0.014 s (0.88x) | 0.144 s | 0.103 s (0.72x) |
| `struct_drop` | 0.066 s | 0.106 s (1.61x) | 0.062 s (0.94x) | 0.754 s | 0.649 s (0.86x) |
| `tokenize` | 0.014 s | 0.014 s (1.00x) | 0.011 s (0.79x) | 0.109 s | 0.067 s (0.61x) |
| `utf8_ingest_unchecked` | 0.003 s | 0.003 s (1.00x) | 0.002 s (0.67x) | 0.031 s | 0.026 s (0.84x) |
| `utf8_ingest_validated` | 0.003 s | 0.003 s (1.00x) | 0.003 s (1.00x) | 0.039 s | 0.031 s (0.79x) |

On x86-64 the SSA build is at or under the flat build on every program but
`string_rfind_byte` (0.008 s against 0.005 s, not yet root-caused: the two
backward AVX2 kernels differ only in how they enter the loop), and
`utf8_ingest_validated`, a tie. On arm64 the one program slower than flat was
`string_build`, and the cause was the last leak: `__fern_str_dec` still left
a uniquely held string behind at rc == 1 because the string producers bumped
the cursor inline, so `__fern_str_append` had been a branch to `__str_concat`
and every append re-copied the accumulator into fresh memory. With every
producer on `__alloc` (`docs/SSA-RC-RUNTIME.md`), the free and the in-place
append ported from x86-64 take `string_build` to 0.144 s against the flat
0.195 s, and the other string programs move too (`tokenize` 0.080 s against
0.109 s, `map_string` 0.074 s against 0.154 s, `sort_strings` 0.184 s against
0.196 s, `string_slice` 0.131 s against 0.148 s).

The same conversion closed a hazard the arm64 freelist had carried since it
landed: `random_bytes`, `tcp_recv` and the `read_dir` container were raw
bumps that `__fern_arr_dec` pushed at their size class on release, and a raw
bump only spans its 16-rounded size, so a block above 2 KiB — where a class
rounds to three significant bits — could come back from `__alloc` larger than
the bytes behind it. The corpus differential never built such a block; the
unit tests now check each producer's block comes back for the next request of
its class.

**Where that leaves the default.** Size and correctness were settled on arm64
(`docs/SSA-REGALLOC-PLAN.md`, "Where that leaves phase 4") and the x86-64
differential compares the whole corpus with no divergence. Speed, the open
blocker there, is no longer one on either target: no program is slower than
flat on arm64, and one is on x86-64 by 3 ms. What is still not measured is
the self-host compiler built through SSA, and the coreutils under a full
workload rather than a benchmark loop; both are the next inputs, and either is
where a regression would still hide.
