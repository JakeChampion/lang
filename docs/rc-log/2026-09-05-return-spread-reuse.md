# 2026-09-05 — the return-position struct update reuses the box it spreads

`return T { ...p, f: v }` allocated a fresh box, retained every carried field
into it, and deep-dropped p's box on the way out. It is the state-threading
shape every self-host emitter is built out of — `LowerState.emit` is one, and
the emitted compiler holds 2,414 call sites of that one method — and #8179
tracks its drop half, 1.03 G Ir inclusive of a 30.8 G `checker.fern` compile
here.

## The routing question #8179 asks first, answered

The drops are in the NATIVE-built `bin/fern-selfhost`, so the fix is
`internal/ir` (#4451 debt). Direct evidence rather than the symbol census: in
`bin/fern -target x86-64-linux examples/self_host/fern.fern`,
`__fn___method_irlower__LowerState_emit` calls `__fern_alloc` once and ends with
`__fn___drop_struct_irlower__LowerState(s)`.

That tail is also the correction to the issue's sizing. #8179's comment reads
the receiver as borrowed and route 1 as an ownership change across the call
boundary at 2,340 call sites. It is not: `s` ESCAPES through the spread, so
`paramVerdict` classifies it **Owned**, the caller retains an argument it keeps
(`calleeParamOwnedByDefault`) and moves one it does not
(`computeOwnedArgMoves`), and the callee's exit sweep already frees the box.
The frame owns the box; nothing about the ABI needed to move. What was missing
was only that `tryStructReuseOverwrite` fires on an ASSIGNMENT target and this
shape has none.

## What changed

`computeReturnSpreadReuse` admits a `return T { ...p, … }` whose base p the
frame owns (`freeEligible` + `frameOwnsIdent`) and cannot read again, and
`emitStructUpdateReuse` — `tryStructReuseOverwrite`'s body, now shared — writes
the changed fields into p's own box under the runtime `__fern_rc_is_unique`
gate. The consuming form differs from the self-overwrite in one op: instead of
rebinding p's slot to the result it zeroes it, so the exit sweep meets a null.

Deadness is the whole soundness argument on the static side, and two things
break it, so both refuse: a `defer` runs after the return value is built and
can name p, and a lambda can hold p past the frame (`deferOrLambdaNames`). A
moved or shadowed p refuses too.

The runtime gate is what covers the caller: a receiver the caller keeps was
retained at the call site, so rc>1 there and the site declines to a fresh box —
`method_recv_survives` / `free_param_survives` / `address_taken` in
`internal/e2e/rc_return_spread_reuse_test.go` are that leg on all three
backends.

### Field eligibility became per-site, which is what let it fire at all

`LowerState` has `ok: boolean` and `selfrebind: string`, and
`structReuseEligible` refused both — the measured struct did not qualify for its
own optimisation. A CARRIED field is neither read nor written on the reuse
branch, so its type cannot matter there; only a REPLACED field rides a temp.
`structUpdateReusePlaceable` says exactly that, and carried fields now leave
the temps entirely — the fresh branch copies them straight out of p's box
(`emitCopiedFieldInc`, shared with the plain spread lowering) rather than
through a slot. Bool joined the placeable set on its own merits: it is a scalar
with no rc, and `reusePlaceableField` is now the one definition both the
per-type and the per-site gate read.

## Measurement

`checker.fern` compiled by a `-g` native-built self-host compiler, callgrind Ir
(deterministic), the same self-host source built once by the base `bin/fern` and
once by the new one:

| | base | new |
|---|--:|--:|
| total Ir | 30,819,647,499 | **28,045,034,138 (−9.00%)** |
| `__fern_arr_dec` self | 1,442,396,611 | 841,828,276 |
| `__fn___drop_struct_x86_native__X86Asm` self | 459,872,804 | 15,389,373 |
| `__fern_drop_arr_str` self | 911,098,859 | 568,351,861 |
| `__fern_rc_dec` self | 371,735,328 | 205,342,284 |
| `__fern_alloc` self | 681,993,179 | 557,338,376 (+56 M in `__fern_alloc_reuse`) |
| `__fn___drop_struct_irlower__LowerState` inclusive | 1,029,315,574 | 833,453,905 |

Whole compiler, emitted asm: 8,037 `__fern_alloc` call sites to 7,429, and 154
`__fern_alloc_reuse` to 762.

The issue expected −3 to −5% from LowerState. The LowerState drop is only 196 M
of the 2.77 G: the shape is everywhere, and the biggest single beneficiary is
the ASSEMBLER's `X86Asm` — threaded through `x86_gas_*` per emitted line, its
drop falls 97%. Read the per-symbol rows above as the general result they are,
not as LowerState's.

`scripts/cliff-bench` on the same subject is unchanged at 270,635 crossings /
171,370,720 bytes: the reuse changes the BOX's count, not a field buffer's — a
replaced array field's displaced reference is handed back by the same drop that
the in-place grow's rc-2 bump pays for. (The committed baseline reads 403,754 /
177,799,608, which is drift already on main; the base compiler built here
reproduces the new numbers exactly.)

The self-host compiler's own OUTPUT is byte-identical: `-emit asm` on
`checker.fern` from the base and the new compiler `cmp`s clean. Only the
machine code the native compiler emitted for it moved.

## What the witness is

`__heap_bump_bytes` cannot see this: the box the reuse skips is the size class
of the box it repurposes, so the freelist recycles it and both forms read flat.
The shape tests pin the gate instead (`internal/ir/return_spread_reuse_test.go`
— an admitted body has one `__alloc_reuse` and no `OpAlloc`, and every carried
retain sits under the fresh-alloc guard), and the differential cases pin the
ANSWER on x86-64, arm64 and wasm.

## The self-host mirror is missing, and why

This is native-only surface for now. The self-host compiler has no
owned-by-default parameter at all — `emit_dec_sweep_except` asserts the
opposite invariant ("params are BORROWED — the caller retains ownership") and
there is no caller-side retain to pair with a consuming callee. Mirroring this
therefore needs the ladder
`docs/rc-log/2026-09-04-param-owned-by-default-is-an-abi-blocker.md` records as
blocked on the whole-program param-escape fixpoint, and no sub-slice of that is
independently sound. Until it lands, stage-2 output keeps the fresh box; the
native-built compiler — which is what #8179 measured — does not.

## Next lead

The same box is claimable one position wider: `var t = T { ...p, f: v }` where
p is dead from that statement on. The emitter already takes a `consume` flag,
so what is missing is the deadness proof — computeReuseSources' block walk has
one, but it demands D be dead AT C and here C reads D's every field, so the
walk needs a "dead from C onward, reads inside C excepted" variant. Worth
measuring first: in `irlower.fern` the return form is 122 of the 305 spread
sites, and the hot ones are all returns.

## Trap

The receiver being freed at the callee's exit is what makes the box the frame's
to repurpose, and it is invisible in the source: a Fern reader sees a plain
parameter. Before reasoning about ownership at one of these sites, read
`paramVerdict` (or the emitted tail) rather than the signature — the same
function is Owned or Borrowed depending on whether the program takes its
address anywhere.
