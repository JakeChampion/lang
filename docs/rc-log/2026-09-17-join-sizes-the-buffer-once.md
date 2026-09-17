# 2026-09-17 — join sizes the buffer once

`__fern_arr_str_join` built its answer with `r = r + xs[i]`, which recopies
the whole prefix on every element. O(total²), and the hazard native's own
`__method_Array_join` comment names in the course of explaining why that one
sums the lengths and fills an exact buffer instead. The self-host now does the
same: one pass to size, `__raw_alloc`, a `__memcpy` per piece, one
`__raw_string` out. The result is still a fresh box that does not alias
`xs[0]`, which is the property the accumulator form was written for.

**Measured**, retired instructions under callgrind, x86-64-linux, `-O`,
joining n 32-byte parts with a one-byte separator:

| n | native | before | after |
|---|---|---|---|
| 500 | 129,388 | 8,444,891 | 100,105 |
| 1000 | 256,234 | 33,388,271 | 198,491 |
| 2000 | 509,700 | 132,774,902 | 395,122 |

Quadratic to linear: 84x at n=500, 336x at n=2000, and the factor keeps
growing because the shapes differ in order rather than in constant. The
self-host's join is now cheaper than native's, which goes through
`buf_new`/`buf_push` where this is a direct memcpy loop.

Wall clock on the same shape: n=20000 0.623 s → 0.004 s, n=50000 4.162 s →
0.005 s, n=100000 **OOM-killed** → 0.008 s.

Seven utilities emit differently (`dircolors`, `ptx`, `shuf`, `sort`, `tsort`,
`wc`, `yes`); the other 97 are byte-identical. `dircolors` on a 200k-entry
config is 0.208 s → 0.190 s, so none of the seven had join as its bottleneck.
The reason to care is `std/io.read_all_stdin`: it collects chunks and joins
once, which is the idiom the stdlib documents, so this helper is on the path
of every utility that reads standard input.

## Why nothing caught it

Three gates looked at this shape and none could fail.

The corpus cannot: the answer is correct either way, only slow. The
**allocation differential** cannot either, and that is the interesting one —
the intermediates are all freed, so the freelist hands them straight back and
`__heap_bump_bytes` reads **0 KB on both compilers**, before and after, at
n=400 and at n=20000. I added a `string-parts-join` case to it, watched it
pass against the unfixed compiler, and removed it again; a case that cannot
fail is worse than no case, because it reads as coverage. And
`perf-bench-selfhost` measures emitted SIZE, so a runtime asymptotic is
outside what it asks.

What separates the two forms is time and peak memory. So the guard
(`self_host_arr_str_join_scale_test.go`) asserts the answer at a size the
quadratic cannot reach — 100k parts, 8 ms once linear, OOM-killed before. It
was run against the unfixed compiler and fails there with exit -1.

## Trap

This is NOT what makes `tac` unable to `tac` a 62 MiB pipe, which is what led
here. `tac` does not use `read_all_stdin`: `hold_stream` keeps its own
`held = held + c` per 8 KiB chunk, and that fold is quadratic under BOTH
compilers by instruction count. Native survives it at 62 MiB in 0.509 s
because it lowers the self-reassign to an in-place `__fern_str_append`, which
the self-host has no equivalent of — it emits a fresh `__fern_str_concat`,
452,856,741 instructions and 77% of the run at 400k lines, growing 4.24x per
doubling. That is the string counterpart of `append_inplace_names_of` and it
is still open; the profile after this change is identical to the profile
before it, to within 40 instructions.

**Next lead.** An in-place `s = s + piece` for the self-reassign shape, the
string twin of the array exemption. `tac`'s `hold_stream` is also simply
written against the wrong idiom and would be linear on both compilers if it
collected chunks and joined once — worth fixing in the utility as well, but
the compiler gap is what makes the two backends disagree.
