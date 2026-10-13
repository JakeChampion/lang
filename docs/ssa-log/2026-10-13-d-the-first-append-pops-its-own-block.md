# 2026-10-13 — the first append pops its own block

`asm_ir.emit_ir_runtime` and `asm_arm64_ir`: `__fern_arr_push`. Refs #8171.
Emitted bytes change in the runtime helper only.

## The shape

Most arrays start as `[]` and take their first element through
`__fern_arr_push`, which grows the empty receiver to four slots. On the
stage-2 compile of `checker.fern` that is 5.1 M of the helper's 7.5 M slow
calls. Since 2026-10-03 (#11188) the path built no frame, but it still pushed
the value, called `__fern_arr_box` for the four-slot box, popped the value
and wrote the length and the element: about 27 instructions with the shim's
own pop of the class.

## What changed

A four-slot box is a 56-byte block, class 7. The first append pops that
class itself and writes the header the shim would have: the capacity, a
count of one, a length of one and the element. Only an empty class calls the
shim, and the allocation counter's build keeps the call, which counts.
Eleven instructions.

arm64's push had no frameless first-append path at all: its slow path built
the frame for every grow, as x86-64's did before #11188. It gains the same
path, pop and all, with the box call behind a frame only for an empty class.

## Measured

The stage-2 compile of `checker.fern` to a binary under callgrind, each
compiler built from its own tree by itself, both compiling one frozen copy of
main at 6fdb1671, warm certifier memo. The base is the sole-owner release
entry before this one.

| | Ir |
|---|--:|
| base | 14.496 G |
| the first append pops its block | 14.413 G (−0.57%) |

No text machine pins `__fern_arr_push`, so the retained-body controls pass
unchanged; `TestSelfHostArrPushFastPath` pins the pop on both targets.
