# A handler that answers at once costs no task

2026-10-06: `async.call_start`, `call_of`, `call_resume`, `call_cancel`,
`run_task_call`; `sempair.taken_apart`; `serve.__started_again`. Slice 5 of
`docs/NET-P0-MESSAGE-LAYER-PLAN.md` §6.2 (#9853).

## The shape

```fern
let t: async.Task[HttpResponse] = async.task_new(() => handler(req, plat));
match (async.task_start(t)) { ... }
```

The serve loop ran every handler as a task: a closure over the request and
the platform, a `Task` record holding it, and the trampoline's `Done`
status, three boxes per request, so that a handler that parks could be
resumed from the closure. Nearly every handler returns at once.

## The rule

`call_start(f, a, b)` runs `f(a, b)` under a fresh task record through a
trampoline that holds the call's parts, and hands the record back freed when
the call returns. A call that parks is held as a `Call { id, f, a, b }`
(`call_of`, from the `Wait`, which now names its task), and `call_resume`
and `call_cancel` run it on through the same trampoline, so the frames the
park saved are the frames the resume rewinds; a closure in the resume path
would be one frame the park never saved. The classifier knows the trampoline
by its `run_task` prefix, as it knew `run_task`.

Two boxes stayed after that, each for a pairing reason:

- The trampoline's `Done` was kept whole by `call_resume`, whose `return
  run_task_call(...)` hands the status on, after that body had been spliced
  into its one caller and left dead. `sempair.taken_apart` now leaves a dead
  body out: spliced into every caller and named by nothing, what it keeps
  whole is never built. `TestSelfHostPairReturn` gains `fwd_once`, the same
  shape.
- `__serve_start`'s `HandlerDone` sat beside `HandlerParked(flight, wait)`,
  two payloads, which no pairing takes. The wait is a field of the flight
  now. The streamed-body lambda, which returns the start whole and cannot
  be paired, takes it apart and builds it again (`__started_again`), so the
  box is paid on the streamed path, where the handler parks anyway, and
  the loop's own call gets the start in two words.

## Measured

x86-64, `TestSelfHostServeAllocsPerRequest`, a hello handler on one
keep-alive connection:

| Step | Per request |
| --- | ---: |
| Before | 13 |
| `call_start` in place of the closure and the `Task` | 12 |
| A dead body is no caller (the trampoline's `Done`) | 11 |
| The wait in the flight, the start rebuilt in the lambda | 10 |

`TestSelfHostTaskProgramsBalanceTheLeakCensus` gains the scheduler program
as a call: two parks driven through `call_of` and `call_resume`, a call
cancelled at its first park, and one that returns at once, with the census
balanced on x86-64 and arm64.

## What is left on the path

The reactor wait's 792-byte scratch, the read's copy, `__Wire`, the parse's
three, `__serve_produce`'s and `__serve_ready`'s tuples (reaching an
indirect call, so counted as able to park), and the handler's response and
header map: the §6.1 rows less the three this slice took.
