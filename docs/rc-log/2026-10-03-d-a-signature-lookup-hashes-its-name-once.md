# 2026-10-03 — a signature lookup hashes its name once

`checker.Scope.sig_at`. Refs #8171. No emitted byte changes: the
stage0-built compiler before and after emits the fixed older tree
(`compiler/fern.fern` at 1ae9cad, its bindings spelled `let`)
and that tree's `checker.fern` byte for byte.

## What the profile named

`checker.sig_name_bucket` was 135 M of self cost in the stage-2 compile of
`checker.fern`. Four callers asked `has_sig` and then `lookup_sig` for the
same name, hashing it twice: `check_call_expr`'s fall-through to a
declared function, `gcall_sig`, the closure-body lookup and
`settle_shared_literal_typevars`.

## What changed

`sig_at` answers the index of a name's signature, or -1. `has_sig` and
`lookup_sig` are written over it, and the four callers read the signature
at the index they already have.

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind, on
main at 20a6b68c: 20.84 G before, 20.81 G after (−0.13%);
`sig_name_bucket` 135 M to 115 M.

## Witnessed

Both emit identities; the 709 targeted `internal/e2eselfhost` tests of
the earlier entries; `make check-sources`.
