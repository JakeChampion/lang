# A refused callee does not stall its callers

A function with one call site is spliced into its caller.
A caller waits until each such callee of its own is spliced.
The inliner refuses some splices.
One example is a callee that passes a word from a parameter to a call.
The caller then waited in each round, and so did each caller above it.

`std/async` showed this: `RealDriver.wait` keeps its call, so `__wait_watched`, `__block`, `__host_wait_any` and `wait_any` stayed out of line when each had one call site.

`splice_rounds` in `compiler/seminline.fern` now marks such a once callee as settled.
The condition is that two rounds found it ready and a body still calls it.
`awaits` does not wait for a settled callee.
The compiler itself needs 19 rounds, so the limit of the loop is 32.

The release of the stall alone made a second defect visible.
The inferred-donor walk of `semscalarreturn.donors` starts at each construction that the caller keeps whole.
A `Duration` inside a `Some` counted as whole.
The `Option` went to its callee as words and had no box.
`Duration.add` then returned a box that no construction used.
`retained_values` in `compiler/sempair.fern` removes the arguments of a construction that no use keeps whole before the walk starts.

Measurements on x86-64 with `FERN_RC_TRACE=1`.
The program makes 300 calls of `async.wait_any(set, Some(at))`, in 100 calls of a function that builds `set` one time.

| `__host_wait_any` | main | stall release only | both changes |
| --- | ---: | ---: | ---: |
| loop | 602 | 602 | 602 |
| self call | 702 | 902 | 602 |

The compiler compiles itself in 155 to 158 seconds with the change and in 154 to 163 seconds without it.

Issue: #15371.
