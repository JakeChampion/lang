# A carried phi carries its operands

2026-10-06: `ownership.continued`. The compiler compiling `checker.fern`,
x86-64, the shape every state-threading pass is written in.

## The shape

```fern
function walk(s: State, n: i32): State {
  let i: i32 = 0;
  while (i < n) { let v: Value = define(s, i); s = v.s; i = i + 1; }
  return s;
}
```

`s` enters the loop's phi and is consumed through it by `define`. The
ownership inference read the phi's consumption as the phi's alone: a plain
phi's operands were never carried, only a tail-recursion phi's parameter
(`continued`), so `s` stayed lent. The plan then gave the phi a unit by
retaining `s` on the entry edge, and the first round's append found the
caller's box at two counts and copied it whole: once per call, 100 of 100
on the probe, whether the id was read before or after the state. The same
three lines straight-line copied nothing, because the planner takes a
field out of a dying record when the record is reached after it only
through reads of its other fields.

## The rule

A carried phi carries its reference operands: the value a consumer takes
from the phi is one of them, so a parameter that enters the phi and is
consumed through it leaves the frame on that path. A slot phi carries its
parameter as before and its later operands through `slot_carried`. Phis
chain, so the pass runs to a fixpoint. The parameter is counted, the
caller moves its unit in at a last use or retains at an earlier one, and
the entry edge retains nothing.

`semsource` is the consumer the compiler itself runs: `s = stmt(st, s)`,
`s = v.s` in every production loop. The same pass takes the other half of
that file's sharing out at the source: a helper handed a receiver's
`Value` beside the state it already holds had the state at two counts
through everything it produced, so those helpers take the receiver's id;
`binary` and `method_call` hold an id once the state is taken; and the
`keeps` read goes before the production it was beside. Those graphs are
unchanged.

## Measured

`checker.fern` built for x86-64 by a stage-3 compiler (the inference
applied to the compiler's own bodies) against the stage-2 compiler from
main at 9d3616f7, callgrind `Ir`. The copies are `__arr_push_shared_count`
and `__arr_push_shared_bytes` over the same compile; the shared `define`s
are the entries whose state or values array had a count above one, read
under gdb.

| | main | source edits | + this rule |
|---|--:|--:|--:|
| total Ir | 18.021 G | 17.989 G | 17.887 G (−0.74%) |
| appends that copied a buffer with room | 513,058 | 506,243 | 470,560 |
| bytes those copies moved | 110.1 MB | 103.2 MB | 88.2 MB |
| `semsource.define` entered shared | 38,509 | 31,171 | 23,559 |
| `__fern_arr_inc_elems`, self | 188.9 M | 177.6 M | 158.0 M |

The compiler is a fixpoint under the rule (stage 3 and stage 4 are the
same bytes). `TestSelfHostAllocDifferentialX86_64` holds the shape at zero
copies (`record-threaded-through-loop-phi`).

## What is left

- An attempt that may refuse and the retry from the same state
  (`method_call`'s builtin then folded method, 2,649 shared `define`s)
  share the state by design; the first append of the attempt copies the
  values array once per method call.
- `match_chain` (2,343), `payloads` (1,958), `merge_env` (1,895),
  `parameters` (1,844), `iterate_array` (1,814) and `header_phis` (1,704)
  still enter shared; each is its own shape to read.
- 470 K copies remain outside `define`, most in the parser's `settle_*`
  and `parse_*` loops: an append chain on a borrowed parameter through a
  loop phi, the case `2026-09-27-a-loop-appending-through-a-borrowed-
  parameter-copies-nothing.md` left for links through phis.
