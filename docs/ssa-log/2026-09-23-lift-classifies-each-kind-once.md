# The lift classifies each op kind once

`ssa_lift.lift_impl` decided what to do with every op by calling
`prod_kind` (up to twice), `prod_mem_kind` and `prod_float_kind` on its
kind name, and each of those builds a string-list literal and scans it, a
fresh allocation per call. On a stage-2 compile of `lexer.fern` the four
`prod_*` classifiers were 99 M instructions, more than `lift_impl` itself.

`lift_impl` now keeps, per function, each kind tag's name and a
`kind_class` bitmask, filled the first time the tag appears, and branches on
the bits.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, native-built compiler, Ir | 3,375,728,349 | 3,099,581,311 (−8.2%) |
| leakcheck allocations compiling `checker.fern` | 93,104,680 | 90,352,917 (−3.0%) |

`checker.fern`'s emitted text is byte-identical on x86-64, arm64 and
wasm32. Passing the class on into `lift_prod_mem`, which asks
`prod_host_kind` and `prod_call_argc` again for each memory op, moved the
same compile by a further 0.03% and is not taken.
