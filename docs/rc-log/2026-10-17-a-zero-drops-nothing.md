# 2026-10-17 — a zero's drop emits nothing

`ssarc.drops`, `ssarc.drops_less`, `ssarc.null_unit`.

## The shape

When the inliner splices a callee that returns an enum into a caller that
matches on it, each return passes the payloads of the variant it built and a
`zero` for the payloads of every other variant. The plan counts a zero as a
unit the frame holds (`ssaunits.fresh_unit`), so every path that leaves
without reaching the join drops the zeros it carries. Each drop was a call
to the type's release helper on a constant null, which tests the null and
returns.

`std/http/h2.__next_frame` inlines `frame.read`, whose `Result[Read,
FrameError]` comes back from thirteen returns. The path that finds no whole
frame in the inbox, taken once per `Conn.read`, made 13 calls to
`__sem_release_frame__FrameError` and more than 30 to
`__sem_release_frame__Frame`, all with a null argument. In a CPU profile of
the HTTP/2 state machine those two helpers held 9% of the samples.

## The rule

A drop of a value whose definition is a `zero` emits no code. Its local holds
the null word on every path, so the release would do nothing. The plan and
its replay are unchanged: the zero is still a unit, still dropped, and the
certifier reads the missing release as nothing spent on a constant.

## Measured

An in-memory driver of a server `h2.Conn` (one HEADERS frame read and one
hello response per request, 3,000,000 requests), arm64-darwin, five
interleaved runs, user seconds: 3.07 to 3.42 (median 3.12) before, 2.68 to
2.95 (median 2.69) after. Allocations per request unchanged at 55.

## Witnessed

`TestSelfHostZeroDropsNoCall` compiles a matcher over an inlined
`Result[Got, Bad]` for x86-64, arm64 and wasm, counts release calls on a
constant null in the emitted code (9 per target before, 0 after), and runs
the program under the sanitizer with a balanced leak census.
