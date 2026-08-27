# The construction-retain matrix: what is left, and what the last cell taught

*2026-08-26*

Written after #7548 and #7558 took the matrix from 12 leaking cells to 10, then
updated when the enum-array group closed at 9. **The count below is the state at
that moment and has since moved twice over: as of 2026-08-27 the matrix stands at
FOUR leaking cells of 35** — `str_arr__fieldread`, `enum__param`,
`enum_arr__param`, `struct_arr__param`. What survives here is the map of the
causes, which the later records key off; the cell list does not. The per-slice
records are
`2026-08-26-arrstruct-append-built-producer.md`,
`2026-08-26-arrstruct-counted-field-share.md`,
`2026-08-26-arrenum-producer-and-append.md`,
`2026-08-26-arrenum-counted-field-share.md`,
`2026-08-26-arrenum-fieldread-share.md`,
`2026-08-26-arrenum-borrowed-argument.md` and
`2026-08-26-arrstruct-borrowed-argument.md`. This one carries the map of what
remains, so the next attempt starts from measurements rather than a reading.

## The 9 cells as they stood

Five have closed since. `str__local`, `str__fieldread` and `str_arr__local` went
with the counted field share and `str_field_share_read`; `str__param` and
`str_arr__param` with `2026-08-27-counted-param-position.md`. The two `__param`
causes this section names were both right, and both are now recorded in their own
slices — the borrowing-ARGUMENT half in the arrenum and arrstruct
borrowed-argument records, the callee-STORE half in the counted-param one.

| group | cells | note |
|---|---|---|
| `str` | `__local` `__param` `__fieldread` | retain gated on slit_reclaim + the whole-program STRFLDOK verdict |
| `str_arr` | `__local` `__param` `__fieldread` | the same verdict, string-array flavour |
| `enum` | `__param` | the callee's field STORE, not the param slot |
| `enum_arr` | `__param` | same store; its borrowing-ARGUMENT half is closed |
| `struct_arr` | `__param` | same store; its borrowing-ARGUMENT half is closed too |

Two natural groupings, and both are bigger than one cell:

- **The five `__param` cells.** This entry used to blame
  `slot_is_reclaimable_arrstruct` refusing a slot below `s.n_params`. Measurement
  says otherwise: the callee's param slot is not involved, and what leaks is the
  CALLER's local — a constant two objects however many times the callee runs.
  They split into two causes, neither of them one credit:
  `enum_arr` / `struct_arr` lost their element walk merely by being PASSED to a
  borrowing callee — both halves are now closed
  (`2026-08-26-arrenum-borrowed-argument.md`,
  `2026-08-26-arrstruct-borrowed-argument.md`) — while `str` / `str_arr` /
  `enum` are fine as arguments and leak only once the callee STORES the param in
  a struct field. The argument half is no longer part of the question.

  **The store is not the thing to fix.** Measured after the arrstruct slice, on
  both flavours: give the callee a LOCAL source instead of a param and the same
  store is already flat — `var src = mkv(i); var p = P { f: src, n: i }` runs
  500/500 at live 0, and the string twin 400/400. So the store retains and the
  holder's drop releases; the callee is balanced every round. Swap the source to
  a param and it is 104/102 (80 bytes) and 103/101 (32 bytes) — a constant two
  objects over 100 rounds, and they are the CALLER's, not the callee's.

  What is left is therefore a CALLER-side rule, the one both borrowed-argument
  slices deliberately declined to widen: admit a param whose only use is a field
  store into a holder that provably dies in the callee. `"ELB:"` refuses it
  statically today because the class's element rule is `len()`-only, so the
  caller emits a bare buffer dec whatever the callee does with its own share.
  One question for all five cells, and it wants the arm-binding analysis those
  slices name.
- **`str` / `str_arr`, 6 of the 9**, hang off the whole-program STRFLDOK
  verdict, which `struct_routes_field_reclaim_at`'s header calls out as the thing
  every analysis deciding a string-field store must consult. Widest group; wants
  its own scoping pass and a registry-level change rather than a lowering one.

## `enum_arr__fieldread` — shipped

Closed by `2026-08-26-arrenum-fieldread-share.md`. Two findings from it generalise
past the enum-array group and are worth carrying into the `__param` and
`str`/`str_arr` work:

- **A base spread `T { ...base }` mints an uncounted co-owner**, so every counted
  share has to refuse in its presence. `LowerState.spread_sites` answers that
  both by holder name and by field type.
- **The probe suite and the fixpoint have DISJOINT blind spots here, so neither
  one being green is evidence.** `2026-08-26-arrstruct-borrowed-argument.md` is
  the case: eleven hand-built probes, both oracles, and the whole `Arr*` sweep
  all passed on a borrow question that segfaults gen2 in three fixpoint
  programs. Reading `emit_arrstruct_deep_free` argued for the weaker question
  and the argument was sound as far as it went — the gap was a handout the
  compiler's own sources make and no probe reproduces. Run both instruments, and
  when they disagree believe the failing one.
- **`"NODEEP:"` and `"FLDCHECKED:"` are two arms of one verdict.** A block-scoped
  slot deep-drops only on the second, so flipping a box-only slot to a
  deep-dropping one means writing the witness, not merely revoking the marker.
  Two programs differing only in braces measured 600/600 and 600/300.

## Instruments for this class

Established in #7558 and unchanged. In order of what each can see:

- census → **blind, and actively misleading**: removing the move gate took
  `moved_ret` from 500/100 to 500/400, which reads as an improvement and is a
  double free
- `__rc_underflow_count()` → blind, because this class FREES element boxes rather
  than deccing them, so no counter is bumped
- `TestSelfHostStage2FixpointArm64` → caught the arrstruct version of this bug,
  blind to the enum one, because the compiler's own source lacks the shape
- reading the value back → catches it

Any change granting an enum-array release needs the last one: read the payload
after the callee returns, with allocation churn in between so freed memory is
reused, and check the value. The template is the `moved_uaf` case in
`internal/e2eselfhost/self_host_arrenum_field_share_test.go`, which segfaults
(139) against native and interp's 25 when its gate is removed.
