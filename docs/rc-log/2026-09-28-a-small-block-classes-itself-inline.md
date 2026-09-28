# A small block's size class is computed inline

Part of #8920.

## What changed

Every allocation and every free on x86-64 and arm64 called `__fern_capw` to
turn a block's word count into its size class. Up to 256 words the class is
the count itself, and nearly every block is that small. The call was still
ten instructions on x86-64 for an unchanged value: three register saves, a
compare, three restores and the return, plus the caller's push and pop of
the argument.

The callers now make the compare themselves and call only for a larger
block. That is `capw_class` in `asm_ir`, used by `__fern_alloc` and
`freelist_push`, and both arm64 sites. `__fern_capw` keeps its own test, so
it stays correct for any count a future caller passes. Wasm's `$__fern_capw`
is unchanged.

## Measured

Before this change, `__fern_capw` took 2.79% of a self-host compile of
`coreutils/tsort.fern`: 12.1M calls from `__fern_alloc`, `__fern_arr_dec`,
`__fern_str_free` and `__fern_arr_push_owned`.

The self-host compiler compiling `coreutils/tsort.fern` (callgrind, x86-64),
each compiler built by itself:

| | before | inline class |
|---|---|---|
| instructions | 4,357,528,365 | 4,226,463,334 (−3.0%) |
| stage 3 bytes (`-g`) | 13,342,832 | 13,360,072 |

The built `tsort` sorting 20,000 edges over 3,000 nodes goes from 43,492,946
to 42,162,201 instructions (−3.1%), with identical output. Stage 3 = stage 4.

`TestSelfHostSizeClassReuse` builds arrays through capacities up to 1024
elements, on both sides of the 256-word line, twice. The second pass must be
served entirely from the free lists, so an allocation and a free that class a
block differently would show as bump growth. It passes on arm64, x86-64 and
wasm. Under `FERN_SANITIZE`, which quarantines every freed block, it checks
the result and the leak balance only.
