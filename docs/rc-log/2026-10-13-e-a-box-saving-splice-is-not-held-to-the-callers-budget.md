# 2026-10-13 — a box-saving splice is not held to the caller's budget

`seminline.splice_calls`, `splice_round`, `any_shared`,
`shared_spliceable`. Against 2026-10-07-b's shared budget and
2026-10-12-m's reuse splice.

## The shape

`std/serve`'s `__serve_loop` reads `__serve_event`'s `(__Conns, i32,
boolean)` apart, and the loop allocates nothing per hello request only
while that call is spliced: as a call, the tuple is a box, and `conns`
read off it is shared through the request, so each of the nine arrays
the request writes is copied. The splice is a shared one (two callers,
45 instructions, every return a construction), and it was refused by
the caller's budget: the loop stood at 1,484 instructions when the
shared round reached it, and `max_caller_insts` is 1,500. `__serve_park`
(71) was refused the same way at 1,523.

The loop crossed the line by what slice 7 of docs/NET-P6-HTTP2-PLAN.md
adds to it (the pool handed to the host, the upstream table closed at
the exits): about 30 instructions, none of them on the hello path. The
zero was holding on the loop's size, not on anything the request does.

## The rule

A splice of a shared callee the caller reads apart removes a box, not
only a call: the caller holds the box's parts after it, values it held
already, so the register pressure the budget guards against does not
rise with it. `splice_calls` lets such a splice through at any caller
size while the callee is within `max_reuse_shared_insts` (500), and
`splice_round` walks a caller past the budget when one of its callees is
a ready shared function (`any_shared`). A callee grown past 500 by its
own splices stays under the caller's budget: the TLS handshake's
`__client_drain`, shared at 1,525 instructions after its own splices,
would otherwise have gone into `__client_run` twice. Leaves, functions
called once and constructor leaves are gated as before.

## The trap the budget was hiding

With the exemption alone the compiler compiled itself to 12,732,216
bytes, 12% past main's 11,353,304, and took 51 s where main takes 22.
The growth was four functions: `parser.parse_if_chain`,
`semthread.key_of` and `watbin.wat_parse_one` call themselves, each is
called from one other place, so each counted as shared (two callers,
every return a construction), and each copy spliced into its caller
brought a call to it that the next round spliced again: 373 copies of
`parse_if_chain` in `parse_primary_nested`, 356 of `key_of` in
`test_of`, 344 and 338 of `wat_parse_one`. The caller budget had been
bounding that unrolling at 1,500 instructions, which is why it never
showed. `shared_spliceable` now refuses a function among its own
callees (`callees_of`), whatever the budget; a tail call is a loop
before the inliner runs, so this is the non-tail recursion only.

## Measured

x86-64, the hooks of slice 7 in `std/serve`:

| | before | after |
|---|--:|--:|
| `TestSelfHostServeAllocsPerRequest`, per hello request | 10 | 0 |
| `TestSelfHostServeAllocsPerRequestH2`, per request on one connection | 70.8 | 70.8 |
| `__serve_loop`, instructions after the pass | 1,524 | 1,685 |

The loop's 1,685 is `__serve_event` and `__serve_park` in it, and
`fetch.idle` (8) that the budget had also refused. Among the stdlib's
other callers past the budget, `http_parse_request_framed_from` (1,309)
now splices `__chunked_trailers` (302), the one shared callee it reads
apart.

The compiler compiling itself (`make distcheck`, stage2 == stage3):

| | main | this change |
|---|--:|--:|
| stage2 bytes | 11,353,304 | 11,308,864 |
| stage2 seconds | 22 | 22 |

The 0.4% it loses is the unrolled recursion.

## Witnessed

`TestSelfHostSharedSplicePastCallerBudget`: `apart_long` reads `span`'s
record apart in a loop whose body runs to some 2,400 instructions, and
its rounds allocate nothing, as `apart_rounds`'s do; with the pass off
both build the record each round. `recur`, `span`'s shape calling
itself, keeps its body and each round of its two callers builds its
record once. On all four targets.
