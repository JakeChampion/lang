# An enum payload read takes the box's unit

2026-09-21 — `ssaunits.payload_root`, `ssarc.took_payload`. Closes part of #9891.

## What was wrong

Every `variant_get` in the self-host produced a BORROW. A borrow owes a retain
to whatever consumes it, and the retain landed before the uniqueness test the
consuming operation makes, so the test read a count of two on a box nobody else
held:

```
    movq 8(%r11), %r15            # kids = the Branch box's payload
    ...
    movl -8(%r15), %ecx
    addl $1, %ecx
    movl %ecx, -8(%r15)           # kids count: 1 -> 2   (the borrow's retain)
.Lrc39:
    movl -8(%r15), %edx
    cmpl $1, %edx
    jne  .Lrc40                   # 2 != 1, always taken
```

The clone arm then decremented it straight back to 1 and called
`__fern_arr_slice`, so the retain and its release existed only to defeat the
test between them. `Full(xs.with(i, x))` copied the whole buffer on every call,
for the life of the program.

## The measurement

`examples/bench/pvec_with.fern`, callgrind, run-time call counts per callee,
self-host against native on the same source:

| callee | self-host | native |
| --- | ---: | ---: |
| `__pv_with_in` invocations | 476,160 | 476,160 |
| array clones (`arr_slice`) | **476,249** | **0** |
| `rc_inc` | 5,572,693 | 1,687 |
| `arr_inc_elems` | 317,650 | — |
| `alloc` | 1,595,278 | 482,593 |
| retired instructions | 680,946,612 | 226,785,986 |

476,160 is exactly one clone per invocation, and `__pv_with_in` executes two
`.with` per `Branch` and one per `Leaf` — so it was always the first one, the
one on the array that came out of the pattern. The second, on the array the
clone had just produced, stored in place.

A listing could not have said that. `lower_counted_arr_with` emits the clone as
a COLD arm behind the run-time test with the in-place store shared by both
arms, so a static count of `call __fern_arr_slice` cannot tell an arm that runs
from one that does not. An earlier pass at this issue drew the wrong conclusion
from exactly that count.

## The fix

`ssaunits.payload_root` is `projection_root`'s shape over an enum rather than a
record: the box is a unit of this frame's own, it does not leave the block, and
after the read it is reached only through reads of its other slots.
`ssarc.took_payload` then renders `steal`'s body at the read — `is_unique` on
the box, null the payload slot when the answer is yes, retain the payload when
it is no. That is native's `emitOwnedConsumingArmDrop` in shape, not in
instructions: native shallow-frees the box on the unique arm with an explicit
`__fern_box_free`, where this leans on the box's own drop instead.

That drop runs in the same step, since the read is its last use, and it is
already `is_unique`-gated and null-guarded: it walks past the emptied slot and
releases the box. So the two arms balance the same way native's do, with one
fewer emitted call.

Unlike a tuple take, the source is not required to be a box this frame
allocated (`takeable_root`). A tuple take nulls without asking; this one pays
for a weaker source with the runtime test, so a box somebody else also holds
keeps its payload and the read retains, exactly as the borrow did.

## The ordering, which is half the fix

`units_of` derived the tuple takes and the element holds first and the payload
takes last. That order is wrong. `held_elements` asks whether the array a read
came out of is OWNED — and with the payload still a borrow the answer was no,
so `#9849`'s machinery never fired and the element read pinned the array
instead of taking its own unit. The array's uniqueness test then failed for a
second, independent reason. Deriving the payload takes first and folding them
into ownership before the other two is what makes the pvec shape work; with the
take alone and the old order, the emitted code still retained the array.

## What it buys, and what it does not

On a shape where the box reaches the destructure uniquely held:

| | before | after | native |
| --- | ---: | ---: | ---: |
| array clones | 512,000 | **0** | 89 |
| allocations | 1,024,009 | 512,009 | 482,593 |
| retired instructions | 1,206,824,271 | **85,032,289** | 71,199,452 |

16.9x off native, down to 1.19x.

It does not close #9891. `std/pvec`'s boxes do not reach `__pv_with_in`
uniquely held, because the receiver of `PVec.with` is a BORROWED parameter —
`semsource.mode` makes a reference parameter counted only when it is declared
`own` — so `v.root` is a borrow, the counted argument is retained at the call,
and the box arrives shared. The take then takes its shared arm at every level
of the trie and the bench row does not move. Closing it needs the borrow
inference of goal 2: a reference parameter the body consumes becomes counted,
which is native's #4400. That is the next increment.

## A bug found on the way

#9901: an `own` argument that a live local still names is mutated through the
alias, on native and the interpreter both. The self-host answers correctly and
the checker's E051 guard is what native's `ownParamEnumScrutinee` rests on, so
the hole is in the guard. Found while writing a case for the take's shared arm;
there is no legitimate Fern program that reaches that arm while E051 holds,
which is why the case here is the borrowed-box decline instead.

## Gates

`internal/e2eselfhost.TestSelfHostPayloadTakeIR` — four programs through the
production CLI on x86-64, arm64 and wasm, each oracle-checked against the
interpreter and held to an allocation census. Verified failing without the fix:
`allocs=62` against a bound of 32, `allocs=145` against 105. The
borrowed-box case is the soundness pin and its census is unchanged by design.
