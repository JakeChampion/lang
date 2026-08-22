# The RCENUMS exit sweep dereferenced the slot it promised to null-route

#7360. Not a leak entry: the defect was a SIGSEGV, and what it cost before this
was a measurement — `"RCENUM:"`/`"RCENUMS:"` had no collision row in #7358's
table because every conditional-block probe of the class crashed before it
could be measured.

```fern
enum R { Full(i32[]), Empty }
function round(i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var o: R = R.Full([i + 2, i + 3]); t = t + 1; }
    return t;
}
```

| toolchain | exit | leakcheck |
| --- | --- | --- |
| interp / native | 50 | `100/100` **0** |
| self-host x86-64, before | **139 (SIGSEGV)** | — |
| after | 50 | `100/100` **0** |

## Attribution — the sweep's comment asserted the opposite of the op's contract

The function-exit `"RCENUMS:"` sweep read *"slots are entry-zeroed, so an
untaken branch routes null"* — true for every sibling loop in that epilogue,
because their releases funnel through null-guarded runtime decs. This one is
the exception: `emit_enum_variant_drops` opens with `op_variant_is`, which
DEREFERENCES the box for its tag on both register backends
(`movq (%rax), %rax` / `ldr x0, [x0]`). The codebase already knew:
`emit_enum_deep_reinit_store` guards the same call and its comment says
plainly *"op_variant_is reads the box's tag, so it faults on a genuinely null
box"*. The sweep was written against the sibling loops' contract instead.

So the four repro ingredients decode as: rc payload (scalar payloads take the
`"SCENUMS:"` shallow dec, which null-guards), a **conditional** block (a plain
`{ }` always initialises the slot), and **two calls** (call 1 must take the
branch to earn the class its sweep; call 2 leaves the entry-zeroed slot null
for it). Function scope was never reachable: a bare `if`-untaken function-scope
local is the same null — hoisting fixed the probe because *its* branch always
ran.

The emitted-asm confirmation: in the faulting binary the loop-rebind release
carries an explicit `!= 0` guard from its own emitter and the exit sweep,
twenty lines below it, has none.

## The fix, and where it went

One null guard around the sweep's dispatch, in **irlower** — the same
`load / ne 0 / if` idiom `emit_enum_deep_reinit_store` uses — so all three
backends inherit it from the IR rather than each backend growing a check
inside `op_variant_is`. The sweep's redundant post-drop zero left with it
(`emit_enum_variant_drops` already zeroes the slot).

Wasm never crashed and was still wrong: `i32.load` at address 0 reads linear
memory rather than trapping, so the same defect there was a silent
wrong-dispatch hazard — whatever byte sits at 0 could in principle match a
type id and send a null box through payload decs. The IR-level guard closes
that without any backend change.

Every other `emit_enum_variant_drops*` site was checked against null
reachability: the consumed-match frees, the precise drops, the pending-return
entries and the struct-field drops all run on a box the statement just proved
live; the reuse donors keep the box by construction. The sweep was the only
caller that can see an untaken branch.

## What was found alongside

**A STRING payload in the same position is not this bug — it is #7364.** Same
program with `Full(string)`: `150/0`, 3600 live bytes, no crash. `frees=0`
means no sweep was ever emitted, so the class declines the payload kind at
collection — a missing credit, not a missing guard. That it never crashed is
the attribution: a credited sweep would have hit this defect on every
branch-untaken call. Pinned at its measured value by the suite's
`string_payload_still_leaks` row, to move only with the credit.

## Non-vacuity

`internal/e2eselfhost/self_host_rcenum_if_block_sweep_test.go`, 6 cases × 3
backends. Reverting the irlower guard fails **3** — the repro, the two-call
boundary, and the second-return-edge case, all by crash exit, on x86-64 and
arm64 both. The plain-block and scalar-payload controls pass either way and
pin the other direction: their frees moving UP would mean the guard's body
started double-releasing. x86-64 additionally pins exact alloc/free counts,
oracle-confirmed 100/100 balanced on the repro.
