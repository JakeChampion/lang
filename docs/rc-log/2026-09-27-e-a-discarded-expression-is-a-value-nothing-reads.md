# A discarded expression is a value nothing reads

`semsource.discard` lowered an expression statement only when its value was
a call, and refused every other one with "unsupported statement" (#10450).
A discarded tuple or array literal, as in `(w, [w, w + 1]);`, stopped the
whole function under `FERN_SEM_IR_STRICT`.

A discarded call was already a value nothing reads, released by the unit
planner at the call. Any other expression is now lowered the same way, with
`expr`, and released where it is made. Only a call keeps the `statement`
flag, since only a call may be void.

The reclaim tests for discarded aggregates move to the CLI on arm64 and
wasm: `TestSelfHostArrArrDiscReclaim*`, `TestSelfHostNestedTupleReclaim*`,
`TestSelfHostStrTupleReclaim*` and `TestSelfHostTupDiscReclaim*`. Their
heap-flatness and over-release checks pass on the typed path. Three
`strtuple` cases gain the `import "std/i32";` their `to_string` calls need.
