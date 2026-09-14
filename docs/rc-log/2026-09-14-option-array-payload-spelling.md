# A `Some` of an array is spelled as the array

#9190, from the `.with` probe set: `match (o) { Some(xs) => { xs =
xs.append(4); } }` over `var o: Option[i32[]] = Some([1, 2, 3])` bailed the
module with `i32.append`, and `xs.with(…)` with `i32.with`. Reads alone
(`xs[i]`, `xs.len()`) lowered, which is what hid it: the generic 4-byte
`arr_get` / `arr_len` run on any slot, and only the method dispatch keys on
the slot being an array.

## Cause

`some_opt_type` records a `Some(arg)` binding's payload from
`elem_type_tag(arg)`, and that walk deliberately answers a scalar array by
its ELEMENT (`[1, 2, 3]` reads "i32"; so do `[[1], [2]]` and a bare `xs`
ident), because its tuple-element consumers want the bare tag. The slot's
`opt_type` was therefore `Option[i32]`, an inferred claim that beats the
binding's own annotation, and the match arm read `ptag == "i32"` and bound
the payload as a scalar. A call scrutinee (`match (mk())`) never had the
problem: its `Option[i32[]]` comes from the callee's declared return.

The checker's stamp is no help here: `Some(…)` is typed as the bare
`Option` family, so `c.ty` reads "Option" with no payload.

## Change

`some_payload_opt_type` spells an array payload as the array it is,
`array_payload_spelling`: a literal from its first element, nesting when
that element is itself an array (`[[1, 2], [3]]` → `i32[][]`), any other
carrier from the tag the checker stamped on it (`Some(xs)` → the slot's
`i32[]`). The map-array decline is unchanged. The arm then binds through
`ptag_is_array` / `ptag_is_arrarr` exactly as the call-scrutinee form
does: borrowed from the box, the rebind un-sharing away from it.

With the payload bound as an array, its `xs = xs.append(v)` reached the
append arm as a sole owner — the credit excluded an aliased local and a
parameter and nothing else — and took `arr_push_owned`, which on a grow
freed the buffer the Option box still held; the census balanced and the
exit read 99 from the box's own release. A binding borrowed from a box is
no more a sole owner than a parameter is (`append_target_sole_owner`), so
it takes the plain push and its grow leaves the box's buffer to the box.
That is the row the census harness caught and a bare exit-code probe did
not: `__rc_underflow_count()` was read before the frame's releases ran.

## Measured

Self-host x86-64, `FERN_LEAKCHECK=1` at emit, interpreter as oracle:

| shape | before | after |
| --- | --- | --- |
| `Some([1, 2, 3])` local, `xs = xs.append(4)` | bail `i32.append` | 7, 3 / 3 |
| the same, `xs = xs.with(0, 9)` | bail `i32.with` | 9, 3 / 3 |
| `var o = Some([1, 2, 3])` (no annotation), a hundred `.with` | bail | 108, 3 / 1 |
| `Option[i32[][]]` local, `g = g.append([4, 5])` | bail `i32.append` | 8, 6 / 0 |
| `Option[i32[]]` local, reads only | 6, 2 / 2 | 6, 2 / 2 |
| `Option[i32[][]]` local, reads only | 4, 4 / 4 | 4, 4 / 4 |

The 3 / 1 and 6 / 0 rows are the unmatched Option box's leak-only class
(the box is read by a second `match`, or holds a nested array, whose
un-shared copy the box's leak-only drop never reaches); native reads
102 / 2 on the hundred-update row, one clone per update. A payload the
box shares with a local (`Some(a)`) un-shares on its append and the local
keeps its own length (4 / 4). The alias-kind
probe set is otherwise unchanged row for row.

Three refused rows of `TestSelfHostOptAliasBind` /
`TestSelfHostOptionAliasMatchConsumed` moved from 200 to 100 frees per
hundred rounds, and the pins moved with them. Each carries the payload out
of an arm (`match (x) { Some(xs) => { out = xs; } }`) over an alias the
confinement proof refuses, so the box leaks by design; with the payload
bound as a scalar, `out` held the buffer UNCOUNTED and its sweep freed it
out from under that box, which is where the second hundred frees came
from. Bound as an array, the carry-out is a counted reference and the
refused box keeps its payload, exactly what the same shape reads under a
call scrutinee (300 / 100 before and after). Native balances all three.

## The escaping arm binding

CI caught the other half. With the payload bound as the array it is, a
Some-arm binding that ESCAPES the arm — `held = a`, `keep = keep.append(a)`,
`return a`, `total(a)` — takes the array protocol: the store-out and the
alias retain at the destination, the return takes the borrowed-binding
transfer retain, and the binding carries the ownership flag
`emit_borrowed_scalar_array_bind` sets. But the consuming-match candidate
gates (`opt_body_binding_escapes` at function level,
`opt_arm_binding_escapes` per block) refused the local for any escape of
its binding, on the reading that the escapee would read a payload the
drop frees. Refused, the box's own count was never released; bound as a
scalar the uncounted store-out had freed the payload from `held`'s sweep
and the box leaked 40 B a round, and bound as an array the retained
store-out left the payload stranded too: `array_payload_escapes` went from
400 to 200 frees and `arm_binding_escapes_to_an_outer_local` from 200 to
100.

A scalar-element array payload is now admitted without the escape walk
(`opt_payload_binding_escapes`, the enum path's `moved_skip_applies`
line): its binding's every escape is counted or flag-tracked, so the
consuming drop's transfer (`claim_pending_scalar_payload`) pairs with
whichever claim is left. Every other payload kind keeps the walk.

| shape | before #9190 | #9190 | now | native |
| --- | --- | --- | --- | --- |
| `held = a` | 600 / 400, live 8,000 | 600 / 200 | 600 / 600 | 600 / 600 |
| `keep = keep.append(a)` (100 rounds) | 400 / 200 | — | 400 / 300 | |
| `return a` from the arm | 200 / 100 | 200 / 0 | 200 / 200 | |
| `acc = total(a)` | 200 / 0 | — | 200 / 200 | |
| the same from a call scrutinee (`mk(i)`) | 600 / 600 | 600 / 600 | 600 / 600 | |

The pins of `TestSelfHostNestedOptNone/array_payload_escapes` (400 → 600)
and `TestSelfHostNestedMatchBorrowHazards`'s two escaping rows (200 → 300)
move to native's numbers; `option-payload-return` and
`option-payload-call-arg` join the cow table.

Not this entry: an `Option[T[]]` TUPLE element (`var t = (Some([1, 2]),
0)`; `match (t.0)`) still bails the same way, since `opt_elem_tag_from_ty`
admits scalar and struct payloads alone for a tuple slot.

## Gates

Five rows in `TestSelfHostWithCowIR{X86_64,Arm64,Wasm}`:
`option-local-payload-append`, `option-local-payload-with`,
`option-local-nested-payload-append` (each bails on the parent commit),
`option-payload-return` and `option-payload-call-arg` (each leaks on it);
the three moved pins above. Also green: the Option / match / payload /
reclaim families of `internal/e2eselfhost`, the whole-compiler emit-all
fixpoint, the complexity ratchet, `make fmt-check`.
