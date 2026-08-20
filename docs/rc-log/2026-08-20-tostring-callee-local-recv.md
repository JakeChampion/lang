# `.to_string()` on a callee LOCAL never entered the fresh-ret registry

#7193 got a helper returning `n.to_string()` into the fresh-string-return
registry. That receiver is a PARAM. A helper that computes before it formats has
a LOCAL receiver and never entered it, so every caller's binding leaked the
result.

## What was measured

x86-64, `FERN_LEAKCHECK=1`, churn at 200 rounds. Native is 0 on every row.

| helper | before | after |
|---|---|---|
| `fmt(n) { return n.to_string(); }` — PARAM | `allocs=400 frees=398 live=32` | unchanged |
| `fmt(n) { var v: i32 = n*2; return v.to_string(); }` | `frees=0 live=6400` (32 B/round) | `frees=398 live=32` |
| the same, result bound to a local first | `frees=0 live=6400` | `frees=398 live=32` |
| `var v: i64 = …; v.to_string()` | `frees=0 live=6400` | `frees=398 live=32` |
| the same `.to_string()` written INLINE at the call site | `live=32` | unchanged |

32 bytes is the constant residue the working PARAM spelling already carries, so
the gate is per-round flatness, not zero. The INLINE row is what isolates this
to the REGISTRY rather than the lowering: the identical expression is flat when
it is not behind a helper.

One word of difference in the helper — `n` versus a local computed from `n` —
was the whole 32 B/round.

## Mechanism

`str_fresh_ret_fns_of` runs on bare AST with no LowerState, so its proof that a
`.to_string()` receiver is scalar is `tostring_recv_is_scalar_param`, which reads
the `params` list and nothing else. There is a LowerState-based sibling
(`tostring_recv_is_scalar`, which consults slot markers) but the registry cannot
reach it — the registry is built before lowering.

The local's ANNOTATION carries exactly the proof the param's does, and the
function body was already being threaded through
`body_has_nonfresh_str_return_reg` → `str_return_is_fresh_reg_in` for the
bound-local case. `str_return_is_fresh` and `str_return_is_fresh_reg` are each
called from exactly one place, so threading `body` two levels further was the
whole plumbing.

## `decl_scalar_local`: every declaration, not any

The scan has no scopes. A nested block may shadow the name:

```fern
var v: i32 = n * 2;
if (n > 1000000) { var v: string = "x"; return v; }
return v.to_string();
```

so admitting on *any* scalar declaration would credit a helper whose `v` can be
a string box. `decl_scalar_local` requires a witness AND the absence of a
counter-example, which is why it is a pair with `decl_scalar_local_bad` rather
than one recursion — one boolean cannot report which of the two it found.

Refused for the same reason: a `for` binder and a match-arm binding, neither of
which has an annotation to read, and an UN-annotated `var`. Guessing an
un-annotated local's scalar-ness from its initialiser needs the slot markers the
registry does not have; refusing costs a leak where guessing could cost an
over-release.

## Gates

`internal/e2eselfhost/self_host_tostring_local_recv_reclaim_test.go` — 4 flat
cases (per-round flatness at 100 vs 200 rounds) and 3 hazard cases; all 7 run on
x86-64, arm64 and wasm, 25 subtests, 0 skips. Every `want` was adjudicated
against BOTH oracles and folds `__rc_underflow_count() * 100` into the exit code.

Non-vacuity: exactly the **4 of 4** flat cases fail against the parent; all three
hazards pass on both, so the refusal set did not move.

Also green: `internal/e2eselfhost -run 'Str'` (338 s) and
`-run '(ToString|Fresh|Ret|Reclaim)'` (450 s), 0 failures each.

## Next lead

The remaining measured rows all need more than a predicate widening:

* the `lower_map_clone_insert` orphan (192 B/round with no tuple involved) wants
  a flow-sensitive `is_aliased_name`;
* `(i, xs)` — an ARRAY at a bare-ident tuple element, 40 B/round — is #7226,
  where the obvious widening was measured to OVER-RELEASE;
* `Some(m)` is a NATIVE rc gap (self 168 / native 128), a different owner under
  `docs/NATIVE-CONVERGENCE.md`.
