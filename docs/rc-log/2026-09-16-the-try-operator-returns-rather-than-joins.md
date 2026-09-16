# 2026-09-16 — the try operator returns rather than joins

`e?` refused on the semantic path for every operand type: `ssasem.unary_result`
admits `-` and `!` and nothing else, so `?` fell to "operator contract: try_ …"
at 12 sites across 7 corpus modules — `Option[i32]` six times, `Option[str]`
and `Option[f64]` twice each, and a `Result` twice.

**The shape.** `?` is control flow, not an operator, so it is handled before
the operator contract is consulted at all. The operand's tag is tested against
the success variant; the success edge unwraps the payload and the expression
continues with it, and the failure edge REBUILDS the failure at this body's own
result type and returns. Nothing joins, so unlike the checked slice beside it
there is no phi: the value is the success payload, and the block left open is
the success edge. Success is the first variant declared and failure the second,
which is the order the tag test relies on (`docs/TRY.md`); native's E078 has
already enforced both that shape and the `@try` opt-in before any of this runs,
so this boundary re-derives what the lowering needs rather than re-policing the
opt-in.

**The payload.** A failure that carries one is read off the operand with
`variant_get` and handed to the `variant_new` that builds the outgoing failure,
so the value moves rather than being copied, and the operand's own box is the
one the frame accounts for. An `Option`'s failure carries nothing, so that arm
builds `None` from no arguments. The failure's payload type must equal the one
the body's own failure variant declares, and a failure carrying more than one
is refused rather than guessed at.

## Corpus

Whole modules produced: 420 → 425, and **no `try_` refusal remains anywhere in
the corpus** — the leaf closes completely rather than narrowing.
`question_op`, `f64_tryop_widen`, `try_op_in_closure`,
`try_result_struct_payload` and `string_slice_option` join.

`string_slice_option` is the one worth naming: the checked slice landed a
commit earlier and that module still kept the AST lowering, on `s[0:3]?` alone.
Both halves are needed for it, and it is now whole.

## Pinned

`TestSelfHostSemanticSourceRC` gains both edges over an `Option` of a scalar, a
success edge carrying a VIEW off a checked slice so its source has to outlive
it, a failure carrying a counted string that the failure edge moves into the
variant it builds, and a loop running both edges five times over so a leak on
either has somewhere to show. All four targets under the leak check.

The print golden carries the shape: a `variant_is`, a failure block that builds
and returns, and a success block that unwraps and carries on — two returns and
no phi, which is what distinguishes this from every other branching producer
here.
