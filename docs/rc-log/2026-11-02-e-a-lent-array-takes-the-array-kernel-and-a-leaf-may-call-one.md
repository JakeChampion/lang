# 2026-11-02 — a lent array takes the array kernel, and a leaf may call one

`ssarc.lent_kernel_site`, `seminline.lent_to_kernels`. A `sort` lead in
#8278.

## The shape

`__mismatch_bytes`, `__sum_bytes_array`, `__bsd_sum_bytes`,
`__crc32_cksum_array` and `__crc32_ieee_array` take `[u8]` views. A view
has two layouts: a tagged string view, and a plain array. Each call went
to a `__sem_byte_view_*` wrapper. The wrapper tests the tag of every view
and then runs the kernel for that layout.

`sort` compares 4,131,365 pairs of lines on 500,000 words. Each compare
passed two `u8[]` arrays, so the wrapper tested two tags that the compiler
already knew. The wrapper cost 66 instructions a call. The call, the tag
tests and the saved registers were 20 of them.

The inliner did not splice `cmp_bytes` into `compare`. Its leaf rule
accepted a call only to a scalar primitive such as `__clz32`. It did not
accept the `array_lend` that makes the view the kernel reads. So each
compare paid two calls, and each callee saved five callee-saved registers
around the one call it made.

## The rule

When an `array_lend` in the same function defines every view argument of
a kernel call, the call emits the packed array kernel in place. A view
that is a parameter, a phi or a slice still takes the wrapper. Such a view
can be a string view. A `__mismatch_bytes` call with a constant count also
keeps the shared wrapper. The generic kernel in place adds lines there and
gains nothing from the known count.

A leaf body can hold a call to a kernel in `copied_scalar_result`. Such a
kernel reads its arguments and returns a count, an index or a hash. An
`array_lend` whose result only such calls read ends its borrow inside the
leaf, as a read view parameter does. A view parameter that such a call
reads is a read view parameter.

A leaf that calls such a kernel must not loop. The kernel is one
instruction in the semantic graph, but it emits a loop of its own. A loop
around it, copied into each caller, can push the caller past its budget.

## Measured

x86-64, `-O`, instructions from callgrind, `setarch x86_64 -R`.

| | before | array kernel in place | and the leaf rule |
|---|--:|--:|--:|
| `sort` of 500,000 words | 1,161,754,890 | 1,074,996,289 | 967,580,563 |

GNU `sort` retires 777,146,324 instructions on the same input. `compare`
is now one function of 109 instructions a compare, with one cold call to
`keycompare`. Before it was three: `compare` 50, `cmp_bytes` 46 and the
wrapper 66.

The loop clause keeps `http_hello` from a trap. Without it, the `std/http`
helpers that loop around `__memchr_bytes` became leaves and spliced into
`parse_request_framed`. That body grew from 1,759 to 1,916 instructions and
exceeded the caller budget of 1,500 instructions. So it stayed out of
`parse_request_bytes`, and each request built one `HttpFramed` record and
took it apart again. `TestSelfHostFramingAllocs` counted 2 allocations a
request against a pin of 0.

| `http_hello`, x86-64 | before | no loop clause | final |
|---|--:|--:|--:|
| instructions | 22,080,408 | 22,344,429 | 22,080,408 |
| emitted lines | 15,308 | 15,543 | 15,299 |

Each dynamic-count array site carries its own copy of the kernel, where
before all sites shared one wrapper. The constant-count clause keeps that
cost off the sites that gain nothing from it. The perf limits are those of
main, and `ordmap_insert` keeps its lower measured limits.
`docs/benchmarks/lent-kernel-loop-budget-2026-11-02/` holds the ARM64 and
x86-64 controls for each variant.

The compiler does not call these kernels on arrays, but the leaf rule
splices more of its own string-kernel leaves. Stages 2, 3 and 4 of the
compiler match byte for byte.

## Next lead

`compare` still saves five callee-saved registers and spills three values
in its prologue. The only call that needs them is the cold call to
`keycompare`. The allocator assigns one home per value for the whole
function. So a value that is live across a cold call takes a callee-saved
register on the hot path too. The next step is to save and restore
around the cold call alone, or to split the live range at the cold edge.

## Tests

`TestSelfHostLentArrayKernel` checks the asm of both ISAs. A function on
two arrays has no wrapper call. A function on two view parameters keeps
it, and so does a constant-count call. Two leaves that call the kernel,
one on arrays and one on views, are spliced into their callers. A leaf
that loops around `__memchr_bytes` stays a call. The test runs the program on every host
target.

`TestSelfHostFramingAllocs` holds the HTTP request path at 0 allocations.
`TestSelfHostCLIPrunesStdlibImportClosureX86_64` named `starts_with` as a
function the prune must keep. `starts_with` is now a leaf and splices into
`main`. The test now checks that each function a call or a tail jump
reaches is emitted.
`TestSelfHostMismatchBytes` keeps the kernel's results on x86-64, arm64
and wasm.
