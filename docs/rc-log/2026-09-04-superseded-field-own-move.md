# 2026-09-04 — a field of an owned base moves into an `own` parameter when the same statement supersedes it

`a = Asm { ...a, cfi: record(a.cfi, line) }` with `record(own s: CfiState, …)`
was E051 in both checkers: a field read is a borrow, and a borrow cannot be
handed to an `own` position. The recorder therefore borrowed, its spread inc'd
every rule array to rc 2, and every append copied the whole buffer — 282 GB on
a whole-compiler self-host build (#8169). #8172 worked around it by lifting the
state into a local for the whole loop, the third time that idiom was needed
(#6011, #6911). This is the language change behind it (#8186).

## The rule

Both checkers admit the `a.f` argument of exactly this shape, on the rebind
form and the return form:

| condition | why |
|---|---|
| the literal's base is the bare ident `a` | the store, or the return, supersedes the field |
| field f's value is a direct call to a named `own` function with `a.f` directly at an `own` position | the callee consumes what the store replaces |
| `a.f` occurs exactly once in the literal | a second read would see the moved value |
| `a` occurs BARE exactly once, as the base | a bare `a` inside the call, or in another field's initialiser, is a second route to the box whose slot the move empties |
| `a` is an `own` param or a local, never a borrowed param | a borrowed box belongs to the caller, who reads the field back |

Native `SupersededFieldOwnMoveArgs` (internal/checker) and self-host
`ow_field_move_fields` are the one recognition; the IR's `computeFieldOwnMoves`
and irlower's `field_move_keys` key on it too, so an admitted argument always
reaches the call site as an argument the lowering has accounted for.

## The lowering

Uniqueness is a RUNTIME fact, not an analysis: `var b = a` earlier, a capture,
a producer's alias — every one of them would read the emptied slot. So the call
site parks the field value, tests `__fern_rc_is_unique(a)`, and on the unique
branch stores a null over the slot, else retains the value for the callee
(native `emitFieldOwnMove`, self-host `emit_own_field_arg`). Every later
release of the slot — the overwrite drop, the exit sweep, a reuse's old-field
release — meets the null and no-ops under the helpers' below-heap guards. An
admitted argument the analysis does NOT claim (a two-word string field, a base
the frame cannot prove it holds) is retained outright: the callee's exit drop
is paid for either way, and a bare pass would not be.

The self-host claim is narrower than native's, because the self-host has no
return-transfer retain: a local bound from an unregistered call may hold a
borrowed field of the callee's argument. `field_move_bases_of` admits an `own`
param, or a local declared once whose every binding is a struct literal, a
strict-fresh producer call, or a call threading the name through an `own`
position; no lambda mentions it, no `for` or match binds it, and the function
has no defer. Everything else takes the retain.

## Measured

Probe: `step` (rebind), `step_ret` (return), `local_form` (local base, in a
loop), `shared` (a `var keep = a` alias that must keep reading its field), 200
rounds, value check folded with `__rc_underflow_count()`.

| compiler / lane | verdict |
|---|---|
| native x86-64, arm64 (qemu), wasm | exit 0; `FERN_LEAKCHECK` allocs=frees=3542, live 0 |
| native x86-64, arm64 under `FERN_RC_FREE_DEBUG=1` (quarantine) | exit 0 |
| self-host x86-64, arm64, wasm (`TestSelfHostFieldOwnMove*`) | exit 0 |
| self-host x86-64 leakcheck, moved shape | allocs 7765 / frees 2507, live 14.4 MB |
| self-host x86-64 leakcheck, same program with `record` BORROWING | allocs 9015 / frees 2506, live 25.9 MB |

The self-host leak is not this change's: the borrowed shape leaks more under
the same compiler, and a bare `take(Cfi { … }, i)` loop — an `own` param and
nothing else — frees 200 of its 400 blocks there (native: 400 of 400). The
callee-side release of an `own` struct param is the open goal-2 item.

Append-cliff bytes (`__arr_push_shared_bytes`, 4000 rounds of one append
through `record`):

| shape | native x86-64 | self-host x86-64 |
|---|---|---|
| `record(own s)` moved | 0 MB | 31 MB |
| `record(s)` borrowed | 0 MB | 63 MB |

Native was already in place on the borrowed shape: `markSupersededFields`
(#8107) stops bracketing the superseded field, and the callee grows at rc 1.
The self-host halves its copies with the move. The 31 MB that remain are not
the move's: the struct literal RETAINS the `record(…)` result when it stores
it into `cfi` (`call __fn___fern_rc_inc` straight after `call __fn_record` in
the emitted `step`), because a call outside the strict-fresh registry is not
known to hand back a counted reference — the same missing return-transfer
retain that narrows `field_move_bases_of`. The next round's `Cfi` box is then
rc 2, `record`'s own-param reuse takes its shared arm and copies, and the box
never reaches rc 0 — that retain is also the 14.4 MB leak above. An
own-threaded `c = record(c, i)` loop, where the result lands in a slot rather
than a literal field, copies 0 MB and leaks 0 bytes under the same compiler.
So on the self-host the one-statement form is not yet a win over the #8172
lift: the field store's retain of a threaded producer's result is the lead.

## Gates

`internal/ir` `TestFieldOwnMove*` (the move, and the string-field fallback that
retains instead); `internal/checker` `TestOwnGuard*SupersededFieldMove*`;
`self_host_checker_codes_test.go` `own-field-move-*` rows in both checkers;
rc corpus `struct_update_field_move_into_own_param` on x86-64, arm64 and wasm
with the leak legs; `TestSelfHostFieldOwnMove*` on all three self-host lanes,
the x86-64 leg also pinning the `is_unique` before the `record` call in the
emitted asm. Each was run without its half of the change and failed.

## Not done here

The recorder itself still uses the #8172 lift. Rewriting it to the
one-statement form is a separate change with its own `scripts/cliff-bench`
before/after — and on the self-host build it is gated on the field-store
retain above: as measured, `a = X86Asm { ...a, cfi: cfi_directive(a.cfi, …) }`
would put the stage-2 compiler's recorder back on the copying path the lift
took it off.
