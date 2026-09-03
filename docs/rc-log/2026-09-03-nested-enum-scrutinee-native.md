# 2026-09-03 — native: a nested enum payload consumed straight off a call (#7910 (d))

`match (mk(i)) { Some(o) => { match (o) { Some(xs) => … } }, None => … }`
with `mk(i): Option[Option[string[]]]`, 100 rounds, allocs/frees live. Native
first, because it leaked this itself:

| backend | before | after |
| --- | --- | --- |
| x86-64 | 400/0 **16000** | 400/400 `0` |
| arm64 | 400/0 (probe: 450/250 **8000** on the issue's two-match shape) | 400/400 `0` |
| wasm (bump N=50 → N=5000) | unbounded | bounded |

**Base.** The before/after rows above were measured against this branch's
base, 27e0ee4a4. The isolation table below was taken during the
investigation, one base earlier — the shape's verdicts are unchanged
between the two (the four headline probes measure identically on both),
but read its exact byte counts as of that tree.

## Isolating the position (native x86-64, before)

| payload position, consumed by a match on the call | native |
| --- | --- |
| `Result[string[], string]`, `Ok([w(i)])` | 200/200 `0` |
| `Result[string[], string]`, `Err(w(i))` | 100/100 `0` |
| `Option[string[]]` | 200/200 `0` |
| `Result[string, string]` | 100/100 `0` |
| `Option[string]` | 100/100 `0` |
| `Result[i32[], string]` | 100/100 `0` |
| `Option[Option[i32]]` | 200/0 **4800** |
| `Option[Option[i32[]]]` | 300/0 **9600** |
| `Option[Option[string]]` | 300/0 **12800** |
| `Option[Option[string[]]]` | 400/0 **16000** |
| `Option[In]`, `enum In { S(string[]), N }` | 400/0 **16000** |
| `Result[Option[string[]], string]` | 400/0 **16000** |
| `Option[Option[string[]]]` BOUND to a local, then matched | 400/400 `0` |
| `Option[Option[string[]]]` built inline, matched | 400/400 `0` |
| `Option[Option[string[]]]` bound, never matched | 400/400 `0` |

Every single-level position is clean; every NESTED one leaks everything —
outer box, inner box, payload — and only in the direct-call position. The
bound form is clean, so the release exists; it is the direct form's
admission that refuses.

## Why: the inner match's scrutinee read as an escape

`reclaimableMatchScrutinee` frees a fresh call-result box at the join, and
admits a pointer binding only when `bindingConfinedToArm` proves it never
leaves its arm. That proof excuses a field access, an index, and a borrowing
call argument (`xs.len()` is `__method_Array_len(xs)`), and nothing else — so
`o`, used as the SCRUTINEE of the inner `match (o)`, hit the bare-ident leaf
and was an escape. The whole outer match lost its reclaim, and nothing else
owned the box.

A nested `match (name)` reads the payload in place. `nestedMatchConfines`
excuses it when its own arms bind no `@`, destructure no sub-pattern (whose
bindings the arm's list does not carry), and confine each named pointer
binding to its arm — the same rule the outer match applies to its own
bindings, one level down. The join's deep drop then reaches the inner box
through the generated `__drop_enum_` for the instantiation, which
`substituteEnumDecl` already sizes for a nested payload.

The match-EXPRESSION form (`*ast.MatchExpr`, a different arm type) keeps the
old refusal; the statement form is what the shape is written with.

## Witnessed

`TestX86_64NestedEnumScrutineeReclaim` / `TestArm64…` (leakcheck balance
over five nested positions, FreeOn + `__rc_underflow_count()` with the inner
payload read through `.len()` AND indexed inside the arm, bump-bounded) and
`TestWASMNestedEnumScrutineeReclaim` (bump-bounded + underflow).

Found alongside, not moved: `match (Some([w(i)]))` — a direct LITERAL
scrutinee — leaks 300/0 on native and self-host alike; no producer call, so
neither admission sees it.
