# 2026-11-03 — a value crosses a call only when it is live after it

A coreutils lead in #8278, from the `ls` rows of 4000 names.

## The shape

The linear-scan allocator gives each value one interval, from its first
position to its last. The interval has no holes. A value "spans a call"
when a call is inside its interval. Such a value must take a callee-saved
register or a stack slot.

In `key_before`, the sort's comparator, the arguments are read last on the
slow path. That path comes after the fast path in the block order. So
their intervals cover the fast path's call to `__fern_str_cmp`, although
no argument is live after that call. The fast path used callee-saved
registers for values that a caller-saved register can hold.

## The rule

- `ssa.hard_call_positions` marks each position that has a call the
  allocator counts and that the cold-call rules do not make cold.
- `ssalive.call_crossers` walks each block that has such a call backward
  from its live-out set. It marks each value that is live just after a
  marked call. A value that a call defines does not cross that call.
- `regalloc_linear_fixed` takes that set as `crossing`. With a set, a value
  spans a call only when the set marks it. With an empty set, the old
  interval test applies.
- Both native backends compute the set once for each function and pass it.

The walk tracks values, not liveness slots. A liveness slot exists only
for a value that leaves its block. A value that lives inside one block
across a call must also cross that call.

| x86-64, `ls` of 4000 names | before | after | GNU 9.4 |
|---|--:|--:|--:|
| `key_before` | 1,632,306 | 1,590,452 | |
| whole run | 13,613,159 | 13,555,466 | 11,298,193 |
| callee-saved pushes in the program | 2,669 | 2,653 | |
| spill stores in the program | 1,660 | 1,627 | |

## What is still behind in `ls`

The fast path of `key_before` now keeps its values in caller-saved
registers. Its prologue still saves five callee-saved registers, because
the slow path needs them and the frame is the same for the whole
function. Shrink-wrapping is the next lead.

## Tests

`compiler/drivers/ssa_cold_calls_run.fern` (`TestSelfHostSSAColdCalls`)
builds a function with a fast and a slow path. A value that is dead after
the fast path's call does not cross it, and keeps a register with no
callee-saved register to give. A value that lives inside the slow block
across its call crosses that call. The `TestSelfHostSSA*`,
`TestSelfHostRetained*` and `TestSelfHostHarden*` sets ran green.
