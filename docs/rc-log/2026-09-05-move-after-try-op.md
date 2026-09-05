# 2026-09-05 — `?` is an exit, so a move after one is claimed too early (#8442)

`stmtContainsReturn` matched `*ast.Return` and nothing else. Its two callers —
`computeMovedLocals`'s `sawReturn` gate and `walkDominatingExprs` — use it for
the same question: has a path already left the function, so that a later
top-level alias no longer dominates every exit?

`TryOp` is such a path and neither could see it. The `?` lowering deliberately
runs the owned-local dec sweep exactly like the `*ast.Return` lowering, and
that sweep skips locals marked `moved` — so a move placed textually after a `?`
was claimed for the whole function while the error path left through a sweep
that declined to release the local. Neither moved nor dec'd.

The predicate now asks whether the statement can LEAVE the function
(`stmtCanLeaveFunction`) and counts both. `stmtDiverges` is the MUST twin —
"reaching this statement ALWAYS leaves" — and `?` deliberately stays out of it:
it leaves only on `Err`. Both now say which question they answer, so the two
do not drift into looking like the same oversight.

## Measured

x86-64 `-sanitize`, the issue's two shapes, driven down BOTH paths:

| shape | before | after |
|---|---|---|
| bare-ident alias after `?` (`computeMovedLocals`) | `leak 32 bytes in 1 blocks` on Err, clean on Ok | clean on both |
| `own` argument after `?` (`walkDominatingExprs`) | `leak 32 bytes in 1 blocks` on Err, clean on Ok | clean on both |

The rc corpus case `move_after_try_op_releases_on_the_err_path` runs both
shapes 50 rounds each on both paths. Against the pre-fix compiler it reads
`allocs=400 frees=300 live_bytes=3200` on x86-64, arm64 and wasm alike; after,
`400 / 400 / 0` on all three. An Ok-only case reads clean either way, which is
why the case drives the Err path first.

## Trap

The issue proposed fixing `stmtDiverges` in the same breath "so they do not
drift apart". That would have been wrong in the other direction: `stmtDiverges`
is consulted for statements that always leave, and a `?` that takes its Ok arm
falls through. Reading a MAY predicate and a MUST predicate as the same
question is how the blind spot reads symmetrical when it is not.
