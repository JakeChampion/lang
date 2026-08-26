# The construction-retain matrix: what is left, and what the last cell taught

*2026-08-26*

Written after #7548 and #7558 took the matrix from 12 leaking cells to 10, then
updated when the enum-array group closed at 9. The per-slice records are
`2026-08-26-arrstruct-append-built-producer.md`,
`2026-08-26-arrstruct-counted-field-share.md`,
`2026-08-26-arrenum-producer-and-append.md`,
`2026-08-26-arrenum-counted-field-share.md` and
`2026-08-26-arrenum-fieldread-share.md`. This one carries the map of what
remains, so the next attempt starts from measurements rather than a reading.

## The 9 remaining cells

| group | cells | note |
|---|---|---|
| `str` | `__local` `__param` `__fieldread` | retain gated on slit_reclaim + the whole-program STRFLDOK verdict |
| `str_arr` | `__local` `__param` `__fieldread` | the same verdict, string-array flavour |
| `enum` | `__param` | the callee's field STORE, not the param slot |
| `enum_arr` | `__param` | same store; its borrowing-ARGUMENT half is closed |
| `struct_arr` | `__param` | same store, plus the borrowing-argument half still open |

Two natural groupings, and both are bigger than one cell:

- **The five `__param` cells.** This entry used to blame
  `slot_is_reclaimable_arrstruct` refusing a slot below `s.n_params`. Measurement
  says otherwise: the callee's param slot is not involved, and what leaks is the
  CALLER's local — a constant two objects however many times the callee runs.
  They split into two causes, neither of them one credit:
  `enum_arr` / `struct_arr` lose their element walk merely by being PASSED to a
  borrowing callee (`2026-08-26-arrenum-borrowed-argument.md` closes the enum
  half; the struct half is the same shape behind its own escape walker), while
  `str` / `str_arr` / `enum` are fine as arguments and leak only once the callee
  STORES the param in a struct field — which needs the store to retain.
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
