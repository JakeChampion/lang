# 2026-09-05 — a scalar-capture closure handed to a callee releases its env (#8546)

#8545 rerouted a non-elided closure local's exit-sweep drop from the
per-closure `__closure_drop_<name>` thunk to the pair-aware
`__drop_closure_value`. That thunk is only what `emitDec` emits when the
closure has an rc-tracked capture (`hasRcCapture`). A closure whose captures
are all scalar — `var add = (x: i32) => sink + x` with `sink: i32` — is
dropped through the generic `__fern_closure_drop`, which frees the one block
it is handed. On an elided slot that block is the env; on a slot that keeps
its pair — the closure crossed a call as a function-typed argument — it is
the pair, and the env box leaked. The issue's first reduction, one 16-byte
block per call.

Both drops are the same reroute: `ElideClosurePair` now sends either spelling
on a non-elided closure-typed slot to `__drop_closure_value`, and Lower's
worklist seeds that helper whenever a closure local is dropped, so the
per-backend dead-function cull is what removes it when every slot elided.

## Two traps in the same helper name

- `__fern_closure_drop` is also the `[T]` slice header's release
  (`emitSliceHeaderDropOnStack`). Keying the reroute on the callee name alone
  sent every slice header to `__drop_closure_value` — the twelve slice
  fixtures and the three `slice_*` corpus cases failed to link on all three
  backends, because the rewritten op kept the slice drop's `Runtime` flag and
  the cull does not follow a runtime call. The slot's type is the
  discriminator (`slotTypeAt` is `*ast.FuncType`), shared by the reroute and
  the seed.
- The seed cannot live in the pass. The emitters walk the AST `prog.Funcs`
  and pair IR by name, and only Lower's worklist appends the stub decl a
  generated function needs; a helper appended by `ElideClosurePair` is never
  emitted.

## Measured

x86-64 / arm64 / wasm rc corpus leak gates, bytes live at exit:

| case | before | after |
|---|---|---|
| `closure_escapes_return` | 16 / 16 / 16 | 0 / 0 / 0 |
| `closure_capture_passed_to_owned_param` | 64 / 80 / 64 | 0 / 0 / 0 |
| `string_closure_capture_churn_free` | 3200 / 6400 / 3200 | 0 / 0 / 0 |
| `closure_scalar_capture_passed_to_callee_released` (new) | 16 per call | 0 / 0 / 0 |

Conformance census (x86-64): `closure_adder` 1 → 0,
`generic_fn_arg_capturing_lambda` 2 → 0; 487 fixtures, 71 leaking, 7515
unpaired.

## Gate hole closed on the way

The census compared a fixture that could not be built or run as `-1`, which
is neither more nor less than a pin of 0 — so a compiler change that broke the
link of twelve fixtures passed the census with every row green. Unmeasured
fixtures now fail with the build error.
