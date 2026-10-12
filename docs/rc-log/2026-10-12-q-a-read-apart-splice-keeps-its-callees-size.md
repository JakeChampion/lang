# 2026-10-12 — a read-apart splice keeps its callee's size

`seminline.Spliceable.small`, `seminline.splice_calls`. Fixes the
regression #12224 brought to `TestSelfHostServeAllocsPerRequest`.

## What broke

A shared callee is spliced where its caller reads the result apart, and
sempair then returns the parts instead of a box. #12224 let a shared
callee be up to 500 instructions and kept the old limit of 100 for a
read-apart site by measuring the callee there: its current size, after
its own splices. Before, a callee was measured once, when it became
shared, and grew freely after. std/http's `request_head` is one of them:
it grows past 100 once its callees are in it, so its read-apart call in
the serve loop stayed a call, its result became a box, and the serve loop
allocated twice per hello request against a pin of none, on every target.
`handshake.negotiate` went the same way.

## The rule

A shared callee records whether it was within 100 instructions when it
became shared (`small`): at the start for the original shared set, at the
round `grown_shared` promotes it for the rest. A read-apart site splices
a `small` callee, whatever it has grown to since. A callee shared at more
than 100 is spliced only where the caller rebuilds the box it returns.

## Measured

`TestSelfHostServeAllocsPerRequest` (and arm64, wasm) is back to zero
allocations per request; `request_head` and `negotiate` are spliced
again. The serve binary's `http_hello` keeps the instruction count it had
before and its code is the size the baseline now records.
