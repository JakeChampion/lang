# Two ownership models, compared (2026-08-30)

#7786 asks for an ownership signature relation a verifier can hold
*against* the compiler rather than one derived from it. Both halves now
exist, so this compares them over the conformance corpus:

- **`ir.Func.ParamConsumed`** — what the LOWERING decided, from the `own`
  keyword and `rc_analysis.go`'s per-function tables.
- **`ssa.SolveOwnership`** — what the EMITTED body demands, read back out
  of the SSA by an interprocedural fixpoint that knows nothing about how
  the decision was made.

`TestOwnershipSolverAgreesWithTheLoweringsOwnVerdict` already compares
them over the **self-host compiler**: 10,279 pointer parameters, 95.39%
agreeing, 150 solver-only and 324 incumbent-only.

Running the same comparison over the **conformance corpus** — a
different population, and a much larger one — gives 78,945 pointer
parameters at **99.21%**, with 105 solver-only and 517 incumbent-only.
The corpus is not added as a second test: it re-lowers the stdlib once
per fixture and would count it every time, which is why the shipped
differential uses the self-host. It is recorded here because it is where
the explanation below was found.

## The two are not the same question

`ParamConsumed` is an **ABI** claim: the callee was handed a unit.
`Consumed` is a **body** claim: the callee discharges one, which
`demandsUnit` reads as *released, or passed to a consuming position,
without a matching retain*.

A function can satisfy the ABI without doing either — `sort_i32_inplace_asc(own arr): i32[]`
takes a unit and **returns the same buffer**. So exact agreement is not
the target, and an equality gate would be wrong. The floor is a collapse
detector; the residue is characterised instead.

## Three wrong guesses, and what the measurement said

The 517 needed explaining. Each hypothesis was tested rather than
asserted, and the first two were simply wrong:

1. **Discharged by returning the value** — `ReturnBorrowedFrom` would
   name the position. Measured: **0**.
2. **Transferred into a consuming callee** — measured: **0**, and
   reading `demandsUnit` afterwards showed why: it already handles that
   case and returns true for it.
3. **The parameter reaches a phi**, where `aliasesOf` stops. Measured:
   **507 of 517** on the corpus, 98.1%.

That share does not carry across populations: on the self-host it is
**158 of 324**, because that bucket is dominated instead by
`computeConsumedParams` promotions of the big threaded state structs,
which are callee-internal and leave the ABI alone. Both figures are now
reported by the test.

## The fix that made it worse

The obvious follow-up — teach `aliasesOf` to cross phis — is wrong, and
the measurement is the only reason that is known. A phi result IS one of
its arguments on every execution, so following it looks conservative in
the safe direction.

It is not. `demandsUnit` is `released && !retained`, and `!retained` is
**anti-monotone** in the alias set: a wider set finds new retains as
readily as new releases. Adding the edge moved the disagreement the
wrong way, **517 → 684**, and overall agreement from 99.21% to 99.00%.
The edge was reverted.

The conclusion is a design fact rather than a bug: **reconciling the two
definitions cannot be done by widening the alias relation.** It needs
per-path accounting — which is precisely the cost #7786 records for the
certifier, and why Roc's `arc_certify.zig` carries a join lattice at
all.

## What is not established

The **105** corpus / **150** self-host parameters in the over-release
direction. That is the shape that would
matter — a body releasing a unit its ABI says it never received — but
the top examples are generated map helpers (`__map_dec_value`,
`__map_free_val_cell`) whose entire purpose is to release, so nothing
here says any of them is a defect. The test ratchets the count so a new
one has to be understood before it is accepted; it does not claim the
current ones are sound.

The remaining **10** corpus parameters in the leak direction have no
explanation yet either.

## An unrelated fix found on the way

The differential indexed `sig.Params[i]` straight against
`ir.Func.ParamConsumed[i]`. That holds only while the two numberings
agree, and they stop agreeing under the two-word ABI, where a string
parameter becomes TWO SSA parameters. It reads correctly today because
this test lowers at one word, but it would have gone quietly wrong the
first time anyone pointed it at arm64 or wasm. It now maps through
`ssa.Func.ParamIRIndex`, which exists for exactly this.
