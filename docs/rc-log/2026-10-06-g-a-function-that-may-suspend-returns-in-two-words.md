# A function that may suspend returns in two words

2026-10-06: `suspend.group_end`, `sempair.pairable`. Slice 8 of
`docs/NET-P0-MESSAGE-LAYER-PLAN.md` §6.2 (#9853), the tuples of
`__serve_produce` and `__serve_ready`.

## The shape

```fern
function __serve_ready[D: async.Driver](drv: D, c: __Conns, at: i32, opts: Config, tail: http.HttpFraming, clk: __Clock): (__Conns, boolean) {
  ...
  return (__serve_next(c, at, opts), false);
}
```

Both callers take the pair apart at once, so `sempair` would return it in
two words, as it does `__serve_read`'s. It refused: each function reaches
an indirect call (the chunk producer of a streamed body, the stop
callback), the suspension classifier counts every such function as able
to park once the program has a park in it, and the pairing excluded every
function the classifier marks.

## The rule

The exclusion guarded a real hazard, in the suspend pass rather than the
pairing. A paired call lowers to the call, the store of its result, the
read of the word beside it (`call_word`, `%rdx` on x86-64, x1 on arm64, a
global on wasm) and that word's store. The pass took the call and its
result's store as a site's group, then emitted the unwinding test, a call
to `__fern_task_mode`, before the ops that followed: the word was read
after a call had clobbered its register.

The group now ends past a paired call's word and its store
(`suspend.group_end`), so the read follows the call before anything else
can, and `sempair` no longer consults the classifier. A call that comes
back unwinding stores whatever the register holds into the word's slot and
the frame returns its dummy; the rewind runs the call again and stores the
real word. Nothing else changes: the callee's `ret_word` already set the
register as the last act before its return, inside the normal-mode segment
the pass guards.

## Measured

x86-64, `TestSelfHostServeAllocsPerRequest`, a hello handler on one
keep-alive connection:

| Step | Per request |
| --- | ---: |
| Before | 6 |
| `__serve_produce` and `__serve_ready` paired | 4 |

`TestSelfHostPairReturn` gains a program that parks inside a task through
a paired pair and a paired Result, which must answer right after the
rewind, and calls both, and a function counted as able to park for its
call through a value, in a loop that allocates nothing, on x86-64, arm64
and wasm (the component form, since the task runtime imports wasi:io's
poll).

## What is left on the path

The read's copy and the parse's three: the `Framed` box the loop keeps
whole, `__request_head`'s head record, and the `Length(n)` framing it
holds (a boxed enum in a record field).
