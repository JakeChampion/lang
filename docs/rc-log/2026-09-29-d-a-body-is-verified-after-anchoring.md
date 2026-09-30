# A body is verified after anchoring

`semsource.complete` ran `ssasem.analyze` on each body before `anchor_module`
had attached the anchor table. Without the table a call's result is its own
root. When that result flowed back into a loop phi, the phi was given a parent
defined later in the loop body. The function was then refused with
"dependency unavailable at use" (#10724). The lowering analyses each body
again with the table attached (`ssaunits`), so the early check read a
different set of parents from the one the plan uses.

The check now runs in `anchor_module`, after the table is attached. A body
that fails it is refused there, and `close_module` refuses its callers. The
graph it checks is also the one `copy_returned_views` has rewritten, which the
old check never saw. No body is refused only after that copy: the production
suite and `TestSelfHostSemanticWholeCompilerX86_64` pass with no pin changed.

## Measured

The three rows below were refused before. Each now compiles to the
interpreter's answer and balances under FERN_LEAKCHECK on x86-64:

- the `id` loop: 8 allocations and 8 frees;
- the two-source `keep` loop: 104 allocations and 104 frees;
- #10738's anonymous-slice `keep` loop: 25 allocations and 25 frees, where the
  AST lowering leaks 208 bytes.

This depends on #10732 (the #10726 fix). Without it, the `keep` loops copy views
between arrays that then free the same box.

## Tests

`TestSelfHostSemanticProduction` on x86-64 (with the sanitizer too), arm64 and
wasm:

- `a-loop-phi-over-an-anchored-call-result-is-produced`
- `a-loop-keeping-views-of-two-sources-through-a-call-is-produced`
- `a-loop-keeping-an-anonymous-slice-through-a-call-is-produced`

`strview-escape-arg-safe` in `TestSelfHostStrViewFrameIR*` now lowers too.
