# 2026-10-02 — the propagation kill walks the bound slots

`ir.const_propagate`, `cp_kill_after`. Refs #8171. No emitted byte
changes: the `selfhost-emit-hashes` sweep is 1,965 rows per compiler
with 0 differing against a compiler built from main at cede3aaf, and
the `checker.fern` binaries the two stage-2 compilers emit are
byte-identical.

## What the profile named

`cp_kill_after` was 267 M self of the 33.63 G stage-2 compile of
`checker.fern`: at each `else` and conditional `end` it scanned every
slot of the function for a binding the scope could have written,
64,272 scans over functions of hundreds of slots, where the slots
bound to a constant at any point are few.

## What changed

`const_propagate` keeps `bound`, every slot `has[]` is true for, each
once, plus slots since unbound until the next kill drops them
(`cp_still_bound`); `cp_kill_after` walks that list with the same test
as before. Every bound slot is in the list, so the kill unbinds what
it did.

A first draft replaced the scan by a log of every write since the
scope opened, which also names the set exactly, and measured the same
264 M: a function's outer scopes see every write in it, which
outnumber its slots. The set that is small is the bound slots, not the
written ones.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at cede3aaf and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 33.63 G | 33.42 G (−0.64%) |
| stage 2, `ir.const_propagate` inclusive Ir | 583 M | 369 M |
| stage 2, `ir.cp_kill_after` + `cp_still_bound` inclusive Ir | 267 M | 41 M |

## Witnessed

`TestSelfHostConstExprFold*`, `TestSelfHostConstfold*`,
`TestSelfHostGenericASTFold*`, `TestSelfHostIRStrengthPeephole`,
`TestSelfHostSemanticSourceRC`, the lint ratchet, `make fmt-check`, and
the emit-hash sweep.

## Next

`const_propagate` is 369 M, now its own walk over the ops;
`propagate_fold` 693 M around it. In the checker, `type_from_spelling`
is 766 M inclusive over 424,753 spellings parsed, most of them the same
few dozen, and `settle_shared_literal_typevars` asks `gc_is_typevar_name`
of every parameter spelling of every generic call, 218 M, an answer the
signature can carry. `sig_bucket_from` hashes a byte at a time, 233 M
over 872 k lookups. `ssarc.block_index` is the last linear block search.
