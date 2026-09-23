# Liveness updates its rows in place

Two self-host analyses copied an array on every update.

- `ssalive.compute` started each block from `var out = live_out`, so the
  first `out.with(...)` copied the whole live-out table (blocks × words)
  once per block per pass. It now builds the block's row in a one-row
  scratch array and writes changed words back into `live_out`, which it
  holds uniquely.
- `ssadeps.push_at` borrowed its stack, so `stack.with(d, v)` copied it on
  every push. The parameter is now `own`.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 (self-host-built) compiler, Ir | 1,934,411,299 | 1,859,502,697 (−3.9%) |
| `lexer.fern` compile, native-built compiler, Ir | 2,607,427,292 | 2,591,201,581 (−0.6%) |
| leakcheck allocations compiling `checker.fern` | 88,822,471 | 88,471,578 (−0.4%) |

`__fern_arr_slice`, the copy behind a `.with` on a shared array, fell from
92 M to 26 M instructions in the stage-2 profile. The emitted text for `lexer.fern` is byte-identical.
