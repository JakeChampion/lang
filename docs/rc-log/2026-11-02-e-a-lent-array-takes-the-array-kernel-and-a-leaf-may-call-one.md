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
can be a string view.

A leaf body can hold a call to a kernel in `copied_scalar_result`. Such a
kernel reads its arguments and returns a count, an index or a hash. An
`array_lend` whose result only such calls read ends its borrow inside the
leaf, as a read view parameter does. A view parameter that such a call
reads is a read view parameter.

## Measured

x86-64, `-O`, instructions from callgrind, `setarch x86_64 -R`.

| | before | array kernel in place | and the leaf rule |
|---|--:|--:|--:|
| `sort` of 500,000 words | 1,161,754,890 | 1,074,996,289 | 967,580,802 |

GNU `sort` retires 777,146,324 instructions on the same input. `compare`
is now one function of 109 instructions a compare, with one cold call to
`keycompare`. Before it was three: `compare` 50, `cmp_bytes` 46 and the
wrapper 66.

The leaf rule costs `http_hello`. The `std/http` helpers that call
`__memchr_bytes` are now leaves, and they splice into
`parse_request_framed`. That body grows from 1,759 to 1,916 instructions.
It then exceeds the caller budget of 1,500 instructions, so it stays out
of `parse_request_bytes`. Each request builds one `HttpFramed` record and
takes it apart again, which adds a box, a release and a call.

| `http_hello`, x86-64 | before | array kernel in place | and the leaf rule |
|---|--:|--:|--:|
| instructions | 22,080,408 | 22,008,408 | 22,344,429 |
| emitted lines | 15,308 | 15,423 | 15,543 |

The kernel in place also costs lines. Each array site now carries its own
copy of the kernel, where before all sites shared one wrapper. The perf
lane rows moved by these amounts are re-banked. `ordmap_insert` runs 1.9%
to 2.5% fewer instructions.

The compiler does not call these kernels on arrays, but the leaf rule
splices more of its own string-kernel leaves. The stage-2 compiler grows
from 15,843,808 to 15,861,008 bytes. Its compile of `lexer.fern` moves
from 1,780,210,499 to 1,779,146,313 instructions. Both counts are the
second compile of each binary, after the certifier memo is warm.

## Next lead

The caller budget counts instructions after each splice. So a leaf that
grows a body can push it out of its only caller, as in `http_hello`. A
body with one caller adds no code when it splices. The first lead is to
exempt such a body from the budget, which can recover `http_hello`.

The second lead is `compare`, which still saves five callee-saved
registers and spills three values in its prologue. The only call that needs them is the cold call to
`keycompare`. The allocator assigns one home per value for the whole
function. So a value that is live across a cold call takes a callee-saved
register on the hot path too. The next step is to save and restore
around the cold call alone, or to split the live range at the cold edge.

## Tests

`TestSelfHostLentArrayKernel` checks the asm of both ISAs. A function on
two arrays has no wrapper call. A function on two view parameters keeps
it. Two leaves that call the kernel, one on arrays and one on views, are
spliced into their callers. The test runs the program on every host
target.
`TestSelfHostMismatchBytes` keeps the kernel's results on x86-64, arm64
and wasm.
