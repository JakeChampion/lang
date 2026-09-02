# A parameter's string element stored into a container is a counted share

`make distcheck` (#6644 — the self-host-built compiler recompiling its own
source) died at 12.2 GB with `SIGSEGV` on a read of address 1 in
`irlower.bytes_at`, deterministically, where the native-built compiler needs
4.0 GB for the same compile. `rc-log/2026-09-01-fused-string-box.md` had already
seen the frame and ruled out the fused string free; this entry is the cause.

## The shape

```fern
function reclaimable_credit(asrows: string[], out: string[]): string[] {
    var i: i32 = 0;
    while (i < asrows.len()) { out = out.append(asrows[i]); i = i + 1; }
    return out;
}
```

`reclaimable_names_of` builds the `APOWNED:` rows into `asrows`, hands the
array to this function, and keeps `out`. The self-host's sharing model for a
LOCAL array is to withhold its deep free once an element escapes into another
container (`strarr_unsafe_for`), so the container holds the only release. A
parameter has no such gate: the caller owns it and frees it deep at its own
exit regardless. So every row `out` received was freed under it when
`reclaimable_names_of` returned, its block was recycled as an array, and
`call_arg_borrowable` read the array's length word as a string's data
pointer.

A struct parameter stored this way was already a counted store
(`struct_alias_ident_escapes`, #7853); a string element was not.

## How it was found

The self-host binary is stripped and its runtime has no frame pointers, so
the route was: `-emit asm` the stage1 compiler through gcc for symbols, run
under gdb to get the frame, then a hardware watchpoint on the dead box's rc
word and data word with a backtrace per write. The arena is at a fixed address
so the box's address reproduced across runs. The freeing write came from
`__fern_str_arr_free` — the string-array deep free — three writes after
`__fern_str_concat` created the row inside `arrstruct_credit_rows`. A 60-line
program with the same shape then reproduced it in 2 s
(`conformance/cases/param_strarr_elem_shared`), and the minimisation said
which side mattered: the SOURCE being a parameter (a local source passes; a
struct-field source passes because field reads are already counted), not the
destination, and not how the element was read (index, bound local, `for`).

## The fix

`LocalInfo.str_caller_elem` marks a string slot bound from such a read (`var
e = p[i]`, `for e in p`, a reassign, an alias). `str_param_elem_escapes`
recognises a direct `p[i]` over a `string[]` parameter or a marked local. Four
store sites retain it: the self-append (`out = out.append(v)`), the
expression-position append (`lower_call_append` — which had no element retains
at all), the clone-form append (`lower_arr_append_value`), and the array
literal. A local array's element is deliberately excluded: retaining there
would leak, since its source's free is already withheld.

## Measured

| | before | after |
|---|---|---|
| `param_strarr_elem_shared`, six store forms, self-host x86-64 / arm64 / wasm | 0 / 0 / 0 (every form corrupted) | 63 / 63 / 63 (= native, interp) |
| `make distcheck` stage2 | SIGSEGV at 197–203 s, 12.2 GB | **OOM-killed** at ~155 s, 13.9 GB peak, no fault |
| `TestSelfHostAllocCountMatrixX86_64`, `TestSelfHostLeakMatrixX86_64`, `TestSelfHostLeakCheckAgreesX86_64`, `TestSelfHostHeapBumpFixpointX86_64`, `TestSelfHostRcConstructContainersX86_64`, `TestSelfHostRcAliasIncX86_64` | | green, unchanged |
| `TestFernFixturesSelfHost{X86_64,Wasm}` (456 fixtures) | | green |

So the correctness half of the self-compile is closed and what stands is
memory. Two leak-check-instrumented compilers on the same input
(`checker.fern`, `-emit asm`), the leak line each prints at exit:

| compiler | allocs | frees | live_bytes |
|---|---|---|---|
| stage0 — native-built | 19,645,369 | 12,987,673 | 441 MB |
| stage1 — self-built, this fix in | 19,509,722 | 4,929,815 | 2,958 MB |

Same allocation count, a third of the frees: the self-host's reclaim frees far
less than native's on the compiler's own code. `FERN_RC_TRACE` on the smaller
`lexer.fern` compile, paired by pointer and symbolised through the gcc-linked
stage1 (`nm`, nearest preceding symbol), puts the leaked words in order:

| leaked blocks | words | site |
|---|---|---|
| 66,298 | 35,745,888 | `__fern_arr_push` — the plain (non-owned) push's pre-grow buffers |
| 43,042 | 1,924,312 | `__fern_str_concat` results |
| 3 × 25,464 | 3 × 814,848 | `irlower.assign_targets_into` |
| 25,352 | 1,014,080 | `checker.t_unknown` — a fresh struct result never released |
| 16,186 | 1,424,368 | `parser.Par.with_depth` — the builder rebind's superseded box |
| 13,619 / 13,382 / 8,853 | 980,568 / 1,177,616 / 637,416 | `lexer.Lex.advance_to` / `parser.Par.advance` / `lexer.Lex.advance` |
| 8,197 | 2,623,040 | `irlower.LowerState.emit` |

Half the live words are pre-grow buffers under the plain `arr_push`, which
frees nothing on a realloc; the rest are struct builder rebinds and discarded
struct results. Those are the next slices. The trace helper is 40 lines of
Python (pair `a`/`f` by pointer, bucket the survivors by site); the two traps
were that `nm` on a `-cc gcc` native build has no symbols to offer, and that
two 12 GB gdb runs do not fit one 16 GB host at once.
