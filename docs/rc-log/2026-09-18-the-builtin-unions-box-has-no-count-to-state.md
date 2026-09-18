# 2026-09-18 — the builtin union's box has no count to state

`2026-09-18-a-firing-is-not-a-reuse.md` gave the typed path's reuse pairing a
slot count to match on, and gave a builtin union — `Option`, `Result` — a fixed
answer of 2: a tag word and a payload word, which is what `op_opt_make`
allocates on the register backends (`__fern_arr_box(2)`, unconditionally).

**One lowering feeds all three backends, and wasm does not size that box the
same way.** `emit_wasm_*`'s `opt_make` allocates `$__fern_str_box(8)` for a
narrow payload and `(16)` for an i64 or an f64, and `opt_none` takes the narrow
one; wasm's `$__fern_alloc_reuse` then compares `8 + nf*8` against the block's
recorded size, so a narrow Option box honours `nf = 1` and a wide one `nf = 2`.
Worse, the width is a property of the SITE rather than the type: a
`Result[i64, string]` box is two slots built as `Ok` and one built as `Err`.

There is no single number to give, so `union_slots` gives none. A DECLARED enum
keeps its count where every variant agrees on field count — its box is
`op_struct_make`'s on every backend, which is `8 + nf*8` on wasm and `nf + 1`
words on the register ones, and those agree.

Found by a review bot on the PR, and confirmed against the three emitters
rather than on its word. The register-backend cost is **zero**: the compiler's
own sources emit 390 pairings either way, so nothing was spending a builtin
union's box.

## An open fixpoint divergence, and what it is NOT

`TestSelfHostSemanticWholeCompilerX86_64` failed on the commit before this one,
and only at its last check: the compiler built through the semantic path
compiled the whole tree to 89,755,105 bytes where the driver produced
89,755,104. **One byte in 89 MB**, with byte-identity against the AST-lowered
driver still holding on `lexer`, `parser`, `checker` and `fern` — so the
divergence was in the semantic lowering compiling ITSELF, not in what it
produces for ordinary input.

It does not reproduce on the tree this entry describes: gen1's emit is
identical to the driver's at 89,835,285 bytes. The obvious reading is that
refusing the builtin union fixed it, and **that reading is wrong** — put back
(`layout_option` answering 2 again, everything else here in place) the fixpoint
still holds, at 89,835,284 bytes. So the builtin-union donor is not the cause,
`-backend ssa` is opt-in and off the default route, and the cause is still
open; #9737 tracks it, with the ruled-out list. Recorded rather than left as a
green run, because a one-byte self-compile divergence that stops reproducing is
exactly the shape `docs/TEST-GATES.md` warns about.

Ruled out since: a per-process nondeterminism in either compiler — the obvious
suspect being `__map_hash_seed()`, a random draw per process — is not it, six
emits across the driver and gen1 answering one md5. What is NOT ruled out is
the harness: `e2eharness.BuildSelfHostBin` serves the driver from a
source-hash-keyed binary cache that a warm job can pre-link into a shared disk
cache, so the gate compares a CACHED driver against a gen1 built from it, which
no manual run reproduces.

## The `tuple_set` decline

`2026-09-18-a-tuple-box-is-storage-too.md` made `ssarc` emit `op_tuple_set`,
which `ssa_lift` had no rule for — the op existed and all three stack-IR
backends implemented it, but the SSA lifter's memory vocabulary did not name
it. So `-backend ssa` declined every function carrying one, and
`TestSelfHostSSABackendAgreesWithStackMachine` gates at "every function
through": three of its cases failed on `float.__db_parity64` and
`string.__str_critical_factorization`.

A tuple element sits at `idx * 8` where a record field sits one shape word
further in — the offset `ld_off` already reads a `tuple_get` back at — and both
ride the same eight bytes on these backends whatever their width, so the store
is `st_at` exactly as `struct_set` is. The wide form takes the same arm, as the
wide READ takes the plain load.

## The gate that was missing

A firing is a CALL. The differential suite asserted firing counts, the RC suite
asserted balance, and a pairing that declines is balanced and correct — so
between them they could not tell a reuse from machinery that never fires, which
is how the shape-only pairing passed every suite while saving 82 allocations
for 41,769 lines of emitted text.

Each differential case now also pins the allocations the program makes with the
pairing ON and OFF, under `FERN_LEAKCHECK`. The difference is the reuse itself:

| case | firings | allocs on | allocs off | saved |
|---|---|---|---|---|
| owns-references | 1 | 12 | 16 | 4 |
| shared-donor-degrades | 1 | 6 | 6 | 0 |
| cross-type | 2 | 13 | 18 | 5 |
| tuple-form | 3 | 10 | 16 | 6 |
| union-donor | 1 | 2 | 4 | 2 |
| scalar-fields | 1 | 3 | 6 | 3 |

A case that saves nothing now FAILS unless it is marked `degrades` — the
runtime decline `shared-donor-degrades` exists to witness — and a case so
marked fails if it saves anything. A fixture pinning a pairing that always
declines cannot land again.

`union-donor` is new, and it is the coverage the review asked for: a declared
enum whose variants agree on field count, dying at a call in front of a
same-slot record. Its box becomes the record's, and the program allocates two
fewer blocks than with the pairing off. It is the shape that matters most to
keep watched, since the builtin union beside it is now refused.

Its payload is an ARRAY, and the first draft's string literal was the same
fault this whole series keeps finding: a claim the fixture does not witness.
The comment said the enum's drop helper releases the live variant's children
at the token, and a literal's box is static, so the release is a no-op —
measured, with `drop_children` deleted from `reuse_token`: the string version
answers 2 allocations and 2 frees and PASSES, while the array version answers
4 and 2 with 80 bytes live and trips the balance check. Found by the same
review that asked for the case.
