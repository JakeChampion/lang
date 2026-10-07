# 2026-10-06 — the handle-method match builds no name

`ssarc.handle_method`, the test every direct call goes through on its way
to a lowering, for the `Reader.` and `Writer.` metadata contracts. Refs
#8171. No emitted byte changes: the compiler before and after builds
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern` for x86-64, byte
for byte.

## What changed

The test compared the callee's name against `"Reader." + m` and
`"Writer." + m` for each of thirteen method names: two concatenations,
two allocations and two frees per name tried, for every call instruction
the lowering met, 256 k concatenations on a `checker.fern` compile for the
handful of calls that are a handle method. It now tests the two prefixes
first, which nearly every name fails, and compares the rest of the name
with each method as a view.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.862 G | 16.826 G (−0.21%) |
| `__fern_str_concat`, self | 158.2 M | 141.8 M |
| `ssarc.builtin_site` (handle_method inlines into it), self | 13.9 M | 6.3 M |
| `__fern_str_free`, self | 77.0 M | 69.7 M |

## What is left

`__fern_str_concat` keeps 1.9 M calls. The next callers by count are
`checker.Scope.lookup`, which builds an "undefined identifier" reason for
every miss, including the probes that go on to find the name in another
table, and the emitters' label formatting.
