# A suspending function is certified as its body before the task frame

2026-11-17. x86-64, arm64 and wasm32, with a check on arm64-darwin. The
change is in `irtables`, `ssarc`, `irverifygate`, `rccertify` and the three
backends.

## The gap

`suspend.transform_state` gives a suspending function a task frame. At each
suspend site it saves the live locals and returns, and a prologue restores
them when the task resumes. The certifier does not model the frame, so it
skipped each function that reads `__fern_task_mode`. Over `fern_main`, 59
functions were skipped. Over the 2,749 programs under `conformance/`,
`tests/`, `examples/` and the utilities, 937 were skipped on x86-64.

## The change

- `ssarc` keeps the ops of the function from before the transform, optimized
  as the transformed ops are (`irtables.Unsuspended`). `LowerState`,
  `LowerResult` and `Lowered` carry them to the backend.

- `irverifygate.certify_body_or_refuse` certifies those ops when the body has
  them, and the emitted ops otherwise. The wasm backend uses
  `certify_lowered_or_refuse`, which makes the same choice. The frame saves
  and restores words, and the retains and releases are the same in both
  forms.

- The skip is gone. A function with a task frame and no body from before the
  frame is a refusal. So the `skipped` field of a report, its census field,
  its memo rule and the census row `x86_64/fern_main.skipped` are gone too.

- Two task routines that the transformed prologue called, and that a body
  before the frame calls, now have rows in `rcsigs`: `__fern_task_park` and
  `__fern_task_park_until` borrow their set. The other task routines are in
  the same list for completeness.

## Measured after

The 2,749 programs have 0 skipped functions and 0 findings on x86-64, arm64
and wasm32, and 400 of them on arm64-darwin. The functions that were skipped
add their unproved values to the count:

| target | unproved before | unproved after |
|---|---:|---:|
| x86-64 | 139,592 | 143,185 |
| arm64 | 136,488 | 140,089 |
| wasm32 | 106,302 | 107,755 |

## What is left

- The frame's saves and restores are outside the proof. The transform's own
  tests are the only check that a resumed frame restores each word it saved.
- A callee with an unsure effect, such as `__fern_arr_concat`, has no model
  of "the argument or a new box".
