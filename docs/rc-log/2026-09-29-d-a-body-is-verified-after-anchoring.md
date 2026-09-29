# A body is verified after anchoring

`semsource.complete` ran `ssasem.analyze` on each body before `anchor_module`
had attached the anchor table. Without the table a call's result is its own
root. When that result flowed back into a loop phi, the phi was given a parent
defined later in the loop body. The function was then refused with
"dependency unavailable at use" (#10724). The lowering analyses each body
again with the table attached (`ssaunits`), so the early check read a
different set of parents from the one the plan uses.

The check now runs in `anchor_module`, after the table is attached. A body
that fails it is refused there, and `close_module` refuses its callers.

## Measured

Both rows below were refused before. Each now compiles to the interpreter's
answer and balances under FERN_LEAKCHECK on x86-64:

- the `id` loop: 8 allocations and 8 frees;
- the two-source `keep` loop: 104 allocations and 104 frees.

This depends on #10726. Without it, the `keep` loop copies views between arrays
that then free the same box.

## Tests

`TestSelfHostSemanticProduction` on x86-64 (with the sanitizer too), arm64 and
wasm:

- `a-loop-phi-over-an-anchored-call-result-is-produced`
- `a-loop-keeping-views-of-two-sources-through-a-call-is-produced`

`strview-escape-arg-safe` in `TestSelfHostStrViewFrameIR*` now lowers too.
