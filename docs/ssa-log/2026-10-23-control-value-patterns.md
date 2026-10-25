# Pattern value expressions

Issues #12705 and #12706 align the grammar and the two parsers.
Statement match arms accept expression bodies. An `if let` expression
requires a final `else` and selects a value from its branch blocks.

The implementation uses the match and value block compiler passes.
It adds no SSA operations. The self-hosted parser routes each branch value
through a result local. The checker derives that local's type from the arm
stores and reports incompatible branch types as E031.

The self-hosted interpreter propagates a function return or a labeled loop
exit through the expression's internal closure. It decodes lexical symbols
before it identifies the result local. A source expression with several
pattern alternatives still runs once.

The fixed point, size results and runtime checks are in
[the proof record](../benchmarks/control-value-patterns-2026-10-23/README.md).
All 11 linked driver sizes stay within the unchanged 5% limit.
The final compiler occupies 14,281,376 bytes.
