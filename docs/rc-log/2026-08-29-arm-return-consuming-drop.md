# The return-path release was re-derived from the payload free fn, and could not name a deep one

#7725. The consuming-match drop is emitted after the match statement, so a
`return` inside an arm jumps over it. `optret_pending` exists for that: the
return-path sweep (`emit_dec_sweep_except_list`) re-emits the drop. The entry it
carried was a single character produced by `optret_payload_tag(pfrees[i])` — and
the release is a five-way choice that `pfrees` does not determine.

| entry | post-match emission | return-path emission (before) |
| --- | --- | --- |
| tagged (call-bound) | `emit_opt_tagged_payload_drop` | `emit_opt_payload_drop` |
| nested Option (`inners`) | `emit_optopt_rc_deep_free` | `emit_opt_payload_drop` |
| string / string[] (`pfrees`) | `emit_opt_payload_drop_via` | same |
| struct payload (`stys`) | `emit_opt_struct_payload_drop` | `emit_opt_payload_drop` |
| leak-safe array | `emit_opt_payload_drop` | same |

A nested-Option payload and a struct payload both free through
`__fern_rc_dec`, so both collapsed onto the shallow drop. That drop is not
wrong so much as short: it decs the payload word and the box, which for
`Option[Option[string]]` is both option boxes and not the string, and for
`Option[P { xs: i32[] }]` is the struct box and not `xs`.

## The span

Measured on `7f0b39a`, x86-64, `FERN_LEAKCHECK=1`, 200 rounds, with
`bin/fern -interp` and the native x86-64 backend agreeing on every answer.
The variable is only where the arm returns.

| shape | native | self-host before | after |
| --- | --- | --- | --- |
| `Option[Option[string]]`, arm returns | 400/400/0 | **800/400 live 6400** | 800/800/0 |
| `Option[Option[string]]`, returns after | 400/400/0 | 800/800/0 | 800/800/0 |
| `Option[P { xs: i32[], n: i32 }]`, arm returns | 600/600/0 | **600/400 live 8000** | 600/600/0 |
| `Option[P { … }]`, returns after | 600/600/0 | 600/600/0 | 600/600/0 |
| `Option[string]`, arm returns | 200/200/0 | 600/600/0 | 600/600/0 |
| `Option[string[]]`, arm returns | 400/400/0 | 400/400/0 | 400/400/0 |
| tagged `Option[i32[]]`, arm returns | 300/200 live 3200 | 300/300/0 | 300/300/0 |
| `Option[i32[]]`, arm returns | 400/400/0 | 400/400/0 | 400/400/0 |

That is the whole span: the two rows whose post-match release is deeper than a
box dec. It matches `blockable`, which admits a row when
`innerf.len() > 0 || dsty.len() > 0` on top of the kinds that were already
complete — the same two additions, made without the pending encoding following
them.

The struct row is the half #7725 left open ("which shapes have the
consuming-match drop as their only release — I have not enumerated it"). The
issue's other open question has the same one-line answer: the two option boxes
ARE freed on the return path because `emit_opt_payload_drop` decs the payload
word, and for a nested Option that word *is* the inner box.

## The fix

`optret_opt_entry(slot, r, i)` encodes the row rather than a projection of it —
`#t<pfree>;<errfree>`, `#n<innerfn>`, `#v<pfree>`, `#p<sty>`, `#a` — and the
sweep's dispatch decodes those arms in the order the post-match emissions test
them. `optret_payload_tag` and the `#s` / `#S` reader branches are gone; they
were the projection.

The two sites are alternatives on disjoint paths, never a sequence, so this is
the shape the code has to be in: anything the return path spells more shallowly
than the post-match drop is a leak nothing else picks up, and anything it
spells at all where the post-match drop would not is a double free.

## What the gate is, and why bytes are not enough

`TestSelfHostArmReturnConsumingDrop{X86_64,WasmIR,IRArm64}` — eleven rows, each
the minimal pair plus the encodings the change rewrote without meaning to move.
Every row asserts `__rc_underflow() == 0` **before** its answer, because the
failure mode of this fix is emitting the deep release on a path that already
had one, and
`docs/rc-log/2026-08-29-option-alias-payload-out.md` measured an over-releasing
build reading `300/300 live 0` where the correct one carried an honest leak. A
byte count ranks those the wrong way round.

`rc_inner_matched_still_partial` in the #7718 corpus was this bug, pinned as a
known partial release while it was read as a payload-kind gap; it is now
`rc_inner_matched_balances` and is the only row covering the inner
`__fern_str_free` actually firing (the literal-inner row allocates nothing to
free).

## Next lead

The tagged row above is a NATIVE leak, not a self-host one: a call-bound
`Option[i32[]]` whose producer returns `None` on half the rounds leaks 3200
bytes over 200 rounds on the native x86-64 backend while the self-host balances.
Unrelated to this change and in the opposite direction; filed separately.
