# 2026-10-13 — a once-called box is saved past the budget too

`compiler/seminline.fern`: `saves_box`, `any_box_saving`, replacing
2026-10-13-e's `any_shared`. Against 2026-10-13-e (a box-saving splice is
not held to the caller's budget).

## The shape

`std/serve`'s `__serve_loop` reads `__serve_pass_end`'s `(__Conns,
__Stop, boolean, boolean)` apart. A program serves through one of the
two loops, so the function is called once and is spliced as a once
callee, not a shared one. As a call, the tuple is a box: one allocation
per pass, and `conns` read off it shared through the next request.

Slice 1 of docs/NET-P7-IO-URING-PLAN.md has the loop make its driver from
the config (`__loop_driver`), and the loop crossed `max_caller_insts`
before the once round reached `__serve_pass_end`. 2026-10-13-e's
exemption covered shared callees only, so the splice was refused and the
hello request went from 0 allocations to 1, under epoll and the ring
alike. As in 2026-10-13-e, the zero was holding on the loop's size.

## The rule

`saves_box` says whether a splice saves a box: a shared callee within
`max_reuse_shared_insts`, as before, or any other ready callee within
`max_once_insts` whose every return is a construction, one of them a box
(`returns_boxes`). Leaves return words, so the second arm is a function
called once, whose construction the caller then reads apart, or a
constructor leaf called with literals, whose box goes static.
`splice_calls` lets either through at any caller size and `splice_round`
walks a caller past the budget when one of its callees qualifies
(`any_box_saving`). A once splice removes the callee's body as well, so
the program does not grow by it.

## Measured

x86-64, slice 1 of P7:

| | before | after |
|---|--:|--:|
| `TestSelfHostServeAllocsPerRequest`, per hello request | 1 | 0 |
| `TestSelfHostServeAllocsPerRequestRing`, per hello request | 1 | 0 |
| `TestSelfHostServeAllocsPerRequestH2`, per request on one connection | 61.0 | 60.0 |

The compiler compiling itself (`make distcheck`, stage2 == stage3):

| | before | after |
|---|--:|--:|
| stage2 bytes | 11,374,064 | 11,370,696 |
| stage2 seconds | 24 | 22 |

## Witnessed

`TestSelfHostSharedSplicePastCallerBudget`: `apart_long` now also reads
`span_once`'s record apart, a function it alone calls, in the loop whose
body runs to some 2,400 instructions; its rounds allocate nothing. With
the once arm of `saves_box` off, each round builds `span_once`'s record
(1600100 where the test wants 1600000). On all four targets.
