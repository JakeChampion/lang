# The array tier credits the element reads it can prove (#7867 slice 4)

`arrayParamCounted` refused every `p[i]` on the blanket ground that "an
array element read hands out a live reference un-counted". Three read
shapes are provably not that, and one of them was #7914's projection
leak.

## The runtime shape that forced it

`filter_gate(check_module(...).diags)` — a fresh struct's array field
projected straight into a call argument — stranded 608 B per call. The
machinery for the shape exists end to end (`isOwnedContainerRead` in
`stashOwnedArgTemp`, the field-access container reclaim), and the
control pair proved the gate: the identical projection into a
`ds.len()`-only callee is clean, into an indexing callee it leaks.
The field-read's retain is emitted unconditionally; the release is
admitted only when the callee's parameter is credited; `filter_gate`
indexes, so it never was. The refusal is CORRECT under the
escape-taint model — a post-call release of something the callee might
have kept uncounted is a use-after-free — so the fix is the credit,
not the admission.

## The three credited shapes

- **A scalar element read** — `s + f[k]` on `i32[]` is a value copy,
  the same argument the string tier has always used for `p[i]` bytes.
- **A scalar field projection** — `ds[i].line` copies the scalar out;
  the element reference dies inside the expression. Struct elements
  with a declaration only; a tuple projection stays refused.
- **The push element** — `out.append(ds[i])` reaches
  `__method_Array_push`'s element position, whose retain half
  `emitArrayPush` emits unconditionally for a pointer element
  (`needsRcIncOnAlias`, and an Index read is never a move site).
  Slice 1's soundness argument, one read deeper.

A bare `p[i]` of a pointer element anywhere else still refuses, and
the refusal tests pin the three escape shapes (pointer field projected
out, element returned, element bound).

## Measured

The projection pair: 33/16 live 608 → 33/33 live 0, answer equal to
interp, all three backends. Two more conformance fixtures improved and
re-banked: `audit_std_crypto` 3 → 0, `bytes_writer` 2 → 1 — stdlib
code paying the same refusal. Non-vacuous: with the credit reverted
the new corpus case fails its leak-gate zero pin at 1,824 B (3 × 608).
`internal/ir` full, the three-backend rc corpus, both leak gates, the
census and the certifier (still zero findings) are green.

`util.append_diags` — `a.append(b[i])`, 321 refused sites, the #3
callee in the static census — is this class in the self-host's own
sources; the driver-side payoff stays gated on #7914 item 2 (the
Map-field root release), which is where its containers strand.
