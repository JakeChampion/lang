# `return h.xs` at `string[]` — the missing limb of the return-transfer dup

#7232. A method whose body is `return h.xs` for a `string[]` field, whose result
the caller binds to a local, over-releases from the second call onward.

| compiler | x86-64 | wasm |
| --- | --- | --- |
| native | 0 | 0 |
| self-host, before | **99** (rc underflow) | **99** |
| self-host, after | 0 | 0 |

## The seam

Native decides retain-on-return with one predicate, `needsRcIncOnAlias`
(`internal/ir/rc_analysis.go:3590`): an alias SHAPE gate (ident / field / index)
and a TYPE gate that answers `ast.ArrayType` **element-type-agnostically**. One
line covers `i32[]`, `string[]`, `P[]`, `E[]`.

The self-host had fragmented that one test into a chain of per-element-type
classifiers on the `StmtReturn` arm — `scalar_arr_field_type`,
`struct_arr_field_read_type`, `enum_arr_field_read_type` — and `string[]` matched
none of them, so `return h.xs` fell through to the generic tail: `lower_expr`
(a bare `op_struct_get`), no inc, `op_return`.

The classifier it needed already existed and was already `pub` —
`strarr_field_read_type` (`irlower.fern:3752`) — with no caller on the return
path. The fix is that one line. The dup is on the buffer POINTER, so the
struct/enum arm's soundness argument carries over verbatim: the struct keeps
owning the elements, the caller receives a counted reference to the buffer.

## Why one call is clean and two are not

`__fern_arr_dec` branches on rc: `>1` decs, `==1` frees **and clears rc to 0**,
`<=0` bumps the underflow counter. The field buffer is born rc=1.

- Call 1: `parts` is an uncounted alias, rc stays 1. The exit sweep decs → rc==1
  → **the buffer is freed** while `keep.xs` still owns it. Silent; the detector
  never sees this one.
- Call 2: `parts` aliases the freed block. The exit sweep decs → rc==0 →
  underflow++ → exit 99.

So the first over-release is the premature free and the second is merely the
detection. Any probe for this shape needs **two** calls; a one-call probe reads
as a null result while the use-after-free has already happened.

## Witnessed at fault level

Reverting the one-line change and rebuilding: the probe exits **99 on x86-64**
and **non-zero on wasm**, against 0 on both with the fix and 0 on native. Three
legs added to `internal/e2eselfhost/self_host_arr_return_transfer_ir_test.go`,
the suite that already pins the bare-param-array sibling (#4357).

Reach for the wasm leg through the **`.wat`** route the harness uses, not through
`-o prog.wasm`. Packaging to a `.wasm` collapses every non-zero `main` return to
exit **1** — measured, `return 6` / `41` / `97` / `99` all report 1 — while the
same program emitted as `.wat` and run directly reports each verbatim:

```
fern -target wasm32-wasi -o prog.wasm p.fern ; wasmtime run prog.wasm   # 41 -> 1
fern-selfhost -target wasm32-wasi -emit asm p.fern <stdlib> -o p.wat
wasmtime run p.wat                                                      # 41 -> 41
```

The 97/98/99 protocol the probes use to say *which* fault fired therefore
survives the harness route and is destroyed by the packaging route. A bare
"non-zero" reading off `-o prog.wasm` is still a sound pass/fail, which is all it
was used for here; it just cannot name the fault, and reading it as one would
mis-attribute an rc underflow as a trap.

## The direction it trades into, and the residue it exposes

A retain converts an over-release into at most a bounded leak, never an
under-free. Measured in the one position where the returned value is consumed
without a swept slot (`keep.get().len()` in a loop):

| rounds | 200 | 400 | 800 |
| --- | --- | --- | --- |
| `live_bytes` | 288 | 288 | 288 |

Flat — the inc fires per call but the bytes stranded are bounded by the object,
not the loop. And this residue is **not new and not specific to `string[]`**: the
untouched scalar arm measures the identical shape at the same position
(`i32[]` field, `live_bytes=88`, equally flat, native 0). All four limbs of the
chain share it, because the caller does not sweep a call result it never bound.
That is a separate defect at a different site — filed rather than folded in here,
since fixing it is a caller-side change across all four limbs.

## Trap worth keeping

`live_bytes` alone would have scored the pre-fix state as BETTER: the premature
free at call 1 returns the buffer to the freelist, so the leaking-nothing reading
and the use-after-free reading are the same number. The underflow counter is what
separates them, and it only speaks on the second call.
