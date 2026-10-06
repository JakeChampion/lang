# A constructor called with literals is a static record

2026-10-06: `seminline.constructor_leaves`, `constructor_body`,
`literal_args`. Slice 7 of `docs/NET-P0-MESSAGE-LAYER-PLAN.md` §6.2
(#9853), the handler's response.

## The shape

```fern
pub function ok(body: string): HttpResponse {
  return HttpResponse { status: 200, body: BodyText(body), headers: headers.header_map_new(), trailers: headers.header_map_new() };
}
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
  return http.ok("hello");
}
```

A record literal whose fields are all literals is a static box (the
constant pool, #6149), and a nullary constructor's result was one already.
`http.ok("hello")` was not: `ok` is called from two places in the gate's
server (the hello and the `/count` path), so the splice pass, which takes
scalar leaves and functions called once, left it a call, and inside it
`body` is a parameter. Two boxes per request, the `HttpResponse` and its
`BodyText`; the header maps were static.

## The rule

A constructor leaf is a function that only builds its result from its
parameters and literals: a straight line of blocks, every instruction a
parameter, a literal, a copy, a construction or a call to another
constructor leaf, the result a construction, no view in it. The pass
splices one into a call whose arguments are all literals, where the
construction is of literals alone and lands in the constant pool. A call
with an argument computed at run time stays a call: no construction is
moved into a caller that would build it there instead, so nothing grows
and no count moves. The flags are read off the bodies at each round, since
a function called once spliced into a constructor (as `tag` and `text`
into `card` in the test) leaves it one.

## Measured

x86-64, `TestSelfHostServeAllocsPerRequest`, a hello handler on one
keep-alive connection:

| Step | Per request |
| --- | ---: |
| Before | 8 |
| Constructor leaves at literal calls | 6 |

`TestSelfHostConstructorLeaves`: a card built through three constructors
from literals, 100 rounds, 0 allocations on x86-64, arm64, wasm and under
the sanitizer; the same call with a level from the loop stays a call and
pays its three boxes a round; with the pass off both pay.

## What is left on the path

The read's copy, the parse's three (the `Framed` box the loop keeps
whole, the head record and the header map `__request_head` builds before
`prev` answers), and `__serve_produce`'s and `__serve_ready`'s tuples,
which reach an indirect call and are never paired.
