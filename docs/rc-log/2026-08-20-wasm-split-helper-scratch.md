# The wasm split/lines helpers leaked their own scratch

The lead left by `2026-08-20-builtin-strarr-view-element-free.md` — wasm still
stranding 67200 on an 18-part split after the element boxes were gone — was not
a Perceus gap at all. `$__fern_str_split` and `$__fern_str_lines` were leaking
buffers the IR cannot see, let alone release.

400 rounds of the churn harness, a pair of compilers from the same commit:

| shape | wasm | x86-64 / arm64 |
| --- | --- | --- |
| 18-part split | 67200 → **0** | 0 |
| 40-part split | 124800 → **0** | 0 |
| char split, 101 chars (`split("")`) | 233600 → **0** | 0 |
| `lines`, 4 lines + trailing `\n` | 16000 → **0** | 9600 |
| `lines`, no trailing `\n` | 9600 → **0** | 0 |

## Three causes, all inside the helper bodies

1. **Every growth step orphaned a buffer.** split built its result with the
   plain `$__fern_arr_push`, whose copy path allocates a new buffer and
   abandons the old one. Correct for a shared receiver — that is what the copy
   path is FOR — but split's array is private, so the abandoned buffer is
   unreachable from anywhere, forever.
2. **The initial array had no rc header.** It came from a bare
   `$__fern_alloc (8)`. `$__fern_arr_push` reads the rc word at `[a-8]` to
   choose in-place versus copy; garbage there fails the test, so *every* push
   copied — quadratic behaviour as well as a leak — and the header-less block
   was itself orphaned on the first one.
3. **`lines` leaked twice per call.** It allocates a `"\n"` delimiter block and
   never released it, and it drops the trailing empty segment by DECREMENTING
   len, which puts that element past the end where no element walk will ever
   reach it.

Cause 1's arithmetic is exact, which is what identified it before any code was
read. Orphans for an *n*-part split are the header-less 8-byte block plus each
outgrown buffer (16-byte header + data): n=2 and n=4 fit in the first cap-4
buffer and leak only the 8; n=9 orphans cap-4 and cap-8 → 8 + 32 + 48 = **88**;
n=18 adds cap-16 → 8 + 32 + 48 + 80 = **168**. Measured: 8, 8, 88, 168.

## Fix

Make the helper own what it allocates. `$__fern_arr_box (0)` for the initial
array so the rc word is real, `$__fern_arr_push_owned` for the three append
sites (it frees a superseded buffer at rc == 1, heap-range- and tag-guarded),
and explicit `$__fern_arr_dec` of the delimiter and of the trimmed element in
`lines`. The runtime emitter now pulls the owned wrapper in for
`str_split` / `@uses_str_lines` as well as for `arr_push_owned`.

Nothing at the CALL site could have done any of this. That is the point worth
carrying forward: a leak that scales with a helper's internal growth is invisible
to every reclaim pass, because the IR never names the buffers.

## What the numbers said before the code did

The register backends measured 0 for these shapes while wasm did not, and a
plain `SARR:` array literal of three fresh strings measured 0 on *both* — so
`$__fern_arr_dec_ptr` was demonstrably freeing elements and buffer correctly.
That pair of facts placed the leak inside the wasm helper before anyone opened
it, and the step-shaped part-count table (flat to 4, then 88, then 168) named
growth buffers specifically rather than a per-element or per-array constant.

An ordinary user append loop (`xs = xs.append(i)` × 64) measures 0 on wasm, which
is what says this is not a general `$__fern_arr_push` bug: irlower routes a
proven-unaliased self-append to `op_arr_push_owned` already. Only the helper,
which irlower never sees inside, was still on the plain push.

## Next lead

`lines` on the REGISTER backends still costs 24 bytes per call (9600 over 400
rounds) with a trailing newline, and 0 without — the same trimmed-element leak,
different mechanism. There `__fern_str_lines` is Fern source
(`asmcore.rt_src_str_lines`) that trims with `parts[0:keep]`, and the element the
slice drops is not reclaimed. It is not fixable the same way: the slice shares
element pointers with `parts`, so releasing `parts`' elements would dangle the
kept ones, and there is no spelling in Fern for "release exactly the dropped
one". It wants either a split that can be told how many parts to produce, or
reclaim support for the drop-a-suffix shape.

The test suite's register ceiling for that case is deliberately slack so the fix
does not have to argue with a gate.
