# A borrowed struct param handed back bare is counted

#9203, the `core/bigint` residue behind `od -t fL` and `printf`'s
subnormals: `var c: Big = b.id_or_make(0)` over a fresh `b` read 200 / 0 for
a hundred rounds — both boxes credited by the plan and refused by the struct
credit collector — and every bigint shape built on `if (k <= 0) { return a; }`
read the same.

## Cause

Every refusal traced to one convention: a struct handed back from a callee was
an UNCOUNTED alias. The return path retained a bare borrowed ARRAY param
(`ret-borrowed-param`, native's return-transfer inc) and said in so many words
that structs take no such inc, because a materialised struct call result was
released through an `__fern_rc_is_unique` gate a second count turns off
(#8240). Given that convention every gate was right: `recv_borrow_escapes_via_result`
refused `b` because a `RECVRET:` method's result reached a move position,
`struct_arg_to_handback` refused a name at a bare-returned position, no
collector credited `c` because its callee was not fresh, and
`release_last_use_source` left an `old == q2` box alone since q2 held no count
on it.

## Change

The handback is COUNTED, the way native's `paramCountedRetain` has it:

- **Callee.** `return <param>` of a borrowed struct param or receiver takes the
  transfer retain exactly as the array arm does (`lower_return_param` now
  carries both arms; `ret_struct_param_counted`) — in a function of the class
  below and only there, since the retain and the caller's release are one
  pairing keyed on that registry. The first cut retained in every function:
  `reserve`'s `return b` fast path in the tuple-handback loops (#8678's
  shapes) is a member of nothing, its caller's snapshot rebind kept the box
  as its uncounted snapshot, and every generation leaked a box and a buffer
  (402 blocks at 200 rounds, 4,002 at 2,000). An `own` param is the caller's
  reference moving through and stays on the move-on-return path.
- **Registry.** `cnt_struct_ret_fns` ("CNTRET:" rows of
  `return_fresh_struct_ret_fns_of`, a least fixpoint): every return is
  strict-fresh, a bare non-own unreassigned param or receiver of the return
  type, or a forwarding call to a member. `cnt_handback_params` carries the
  counted "<key>|idx" / "<key>|r" rows; `handback_params` keeps the uncounted
  ones (a callee outside the class), and `recv_borrow_fns_of` keys a counted
  method's bare-receiver return "CNTRECVRET:", which the credit gate does not
  read. The "FRESHSELF:" registry was this class's mixed-path subset and is
  gone.
- **Caller.** A binding from a member earns the struct credit
  (`collect_fresh_ret_call_names` over the union), and every name that is a
  receiver or bare argument at a counted handback position, or is bound or
  assigned from such a call, is "SINKSHARE:" (`sinkshare_counted_handbacks`):
  both may hold a claim on one box, so each walks its fields only on finding
  rc 1. Where the result turns out to BE the argument's own box the extra count
  goes back (`emit_handback_identity_dec`): at a self-rebind (`x = x.m()`,
  ahead of the ordinary release, whose cow guard then sees one box), at a
  dying source's last-use release and the tail release (`old == q2` and not the
  slot's uncounted snapshot), and a read-through or discarded result of a
  member takes the bare box dec either way (`emit_counted_result_release`; an
  array field read off it is admitted, since the buffer outlives the box's
  shallow dec). A credited slot's rebind release gives its count back even when
  the box is its snapshot's (`emit_field_reclaim_store`'s `box_counted`).

## Measured

Self-host x86-64, `FERN_LEAKCHECK=1` at emit, interpreter as oracle, a
hundred rounds:

| shape | before | after |
| --- | --- | --- |
| `var c = b.id_or_make(0)` (the receiver back) | 200 / 0 | 200 / 200 |
| `var held = keepit(b)`, both read | 200 / 0 | 200 / 200 |
| `var c = keepit(b)`, b dead at the bind | 200 / 0 | 200 / 200 |
| `x = x.id_or_make(0)` three times | 200 / 0 | 200 / 200 |
| `x = x.id_or_make(j % 2)`, identity and fresh alternating | 300 / 0 | 300 / 300 |
| `b.id_or_make(0).mag.len() + b.id_or_make(1).mag.len()` | 300 / 100 | 300 / 200 |
| `var c = b.id_or_make(1)` (the fresh path, `make(a.neg, mag)`) | 300 / 0 | 300 / 200 |
| `c = b.id_or_make(0); c = c.id_or_make(1)`, b live | 300 / 0 | 300 / 200 |
| `b.mul_pow10(1)` (the bigint shape) | 400 / 0 | 400 / 300 |
| `take(Big { … })` (an `own` param handed back) | 400 / 400 | 400 / 400 |

The 48 bytes a round still standing on the fresh-path rows are `b.mag`: the
receiver is "NODEEP:" (a method receiver whose callee is proven neither a
borrow nor identity-only, since it reads `a.mag` into a local), so `b`'s
release is box-only and the count the callee's field read added is never
given back. The field-read half, the issue's other bullet, is the next slice.
`alloc_flat_method_identity_return` reads 1,052 / 1,050 and "flat" before and
after.

## Every consumer of a counted result owes the release

The convention's cost is that a counted result binds every consumer, not just
the ones this slice set out to fix — the hazard the SFRRECV entry named when it
chose a runtime pointer compare over an inc ("adding that inc UNPAIRED measures
strictly worse"). The positions a result can die in are enumerable, and the
first cut covered three of them: a binding (the struct credit), a discarded
statement, and a read-through field access. The fourth, an ARGUMENT temp, it
did not: `take(b.me())` left the callee's retain with no owner, so `b`'s box
never reached rc 1 and its fields never went —
`TestSelfHostRecvBorrowDeepDrop/recvident-borrowable-arg-flat` read 98 (the
receiver's fields leaked) on both register legs, where the same shape had been
flat before.

The argument temp is parked and released at the site that already does this for
a fresh struct literal and a strict-fresh producer call
(`stash_fresh_struct_arg` / `free_stashed_struct_args`), with one difference:
the counted temp is stashed UNMARKED, so its release is the bare box dec rather
than the field walk. On the handback path the box is the caller's own and its
fields are not this frame's to free; on the fresh path a returned literal's
field values are not proven owned (`Box { tag: t, … }` takes a parameter's
box). That is the same floor `emit_counted_result_release` keeps one position
over. A strict-fresh producer's argument is classified first and keeps its
deeper release.

The FIFTH is a method RECEIVER — `b.me().val()`, which read the same 98 and was
flat before the slice. Its release lives in `lower_call_struct_method`: park the
lowered receiver, reload it for the call, dec after. What decides whether the
dec may fire is `counted_recv_release_ok`, and it has two tiers. The plain
`recv_borrow_fns` key says the outer body lets the receiver escape nowhere, so
the call cannot hand this box back and the dec frees nothing the caller reads
next. `RECVIDENT:` says the only escape is the bare `return self`; paired with
the callee being a counted member, a handback arrives carrying its own added
count, so the dec still leaves exactly one — which is what makes `b.me().me()`
balance rather than leak one box a round. A bare UNCOUNTED handback earns
neither key and keeps its leak: the safe floor, not a fix.

Counting these five is not a proof the list is closed. Each was found by writing
the shape and reading the round-over-round bump, and the search order was the
order the shapes occurred to someone. What would close it is a consumer-side
audit keyed on `cnt_struct_ret_fns` membership rather than on position, and
nothing computes that today.

## The ssarc caller boundary moves with the registry

`ssarc.caller_sigs` erases every row an AST caller could have written for a
result the SSA boundary lowers instead, and its own comment says the list
mirrors irlower's readers by hand with nothing tying the two together. This
slice added a reader and left the list behind: the dead `"FRESHSELF:"` row is
now `"CNTRET:"`, and because the class is read through two fields of its own
rather than the prefixed row alone, `cnt_struct_ret_fns` and
`cnt_handback_params` are erased for that name too. Without them an AST caller
would still be told the result carries a count — and, for a handback position,
would give back a count the boundary never added. The erase direction is a
leak, never a double release.

## Gates

Four rows in `TestSelfHostWithCowIR{X86_64,Arm64,Wasm}`:
`struct-handback-bind`, `struct-handback-free-fn`, `struct-handback-last-use`,
`struct-handback-rebind-loop` (each allocs / 0 on the parent commit).
`TestSelfHostBorrowedStructParamReturn` (#8240) stands on its answers and
underflow count with the convention reversed.

The argument-temp release has its gate already:
`TestSelfHostRecvBorrowDeepDrop{X86_64,Arm64}/recvident-borrowable-arg-flat`
measures the shape by heap growth and reads 98 without it. The receiver
position takes four rows of the same table, each reading 98 without the
release and 0 with it: `recvident-chain-recv-flat`, `-array` (an rc-array
field under the same chain), `-double` (`b.me().me()`, the RECVIDENT tier) and
`-freecall` (a strict-fresh free producer, which `counted_call_key` reads by
bare name). The ssarc row has
none of its own — the boundary's own tests cover its shape, and the change only
erases rows, whose failure direction is a leak rather than a release the
boundary never earned.

Also green: the struct, leak-matrix, alloc-differential, block-scoped,
reclaim, fresh-producer, literal-argument and ssa / semsource families of
`internal/e2eselfhost`, the three-backend cow table, the whole-compiler
emit-all fixpoint, the complexity ratchet, `make fmt-check`.
