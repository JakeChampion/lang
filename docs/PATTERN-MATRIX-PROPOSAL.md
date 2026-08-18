# Compiling patterns with a decision tree — what the arm-position limit costs, and what replacing the desugar would buy

Status: proposal (2026-08). Not a commitment. Written after finishing the
last patchable slice of [#2698](https://github.com/JakeChampion/lang/issues/2698),
because what remains there is not a patch. Every claim about current
behaviour below was run against the tree, not read off an issue.

Companion reading: `docs/NATIVE-CONVERGENCE.md` (both compilers would have
to move), `docs/TEST-GATES.md` (what gates a pattern change today).

## 1. Short answer

Fern's arm-position pattern matcher is a **parse-time desugar**: a run of
same-variant arms is merged into one arm whose body is an inner `match` on a
synthetic temp. That shape is what forces the three limits in §2, and none of
them can be lifted by widening the desugar — §3 shows why the information the
desugar would need has already been destroyed by the time it would need it.

The replacement is the standard one: compile the whole arm list as a
**pattern matrix** into a decision tree (Maranget 2008, "Compiling Pattern
Matching to Good Decision Trees"). That is a real piece of work — roughly 335
lines of native desugar and 365 of self-host desugar come out, and something
comparable goes in — but it is not speculative, and §6 lists four things Fern
carries today that exist *only* to prop up the current shape and would be
deleted rather than ported.

The strongest argument is §4: **the semantics the arm-position path cannot
express are already shipping on the tuple path.** This is not a question of
what Fern should mean. It is a question of one path being compiled worse than
another.

## 2. The limit, exactly

Three `P001`s, all from `nestedPos` / `desugarNestedStmtArms` in
`internal/parser/parser.go`. Verified messages:

**(a) One nested sub-pattern per arm.**

```fern
match (v) {
    P(Ok2(a), Ok2(b)) => { return a + b; },   // P001
    …
}
// only one nested pattern per payload is supported — use a nested `match`
```

**(b) All arms of a variant must nest at the same payload position.**

```fern
match (v) {
    P(Ok2(a), b) => { return a; },
    P(x, Ok2(b)) => { return b; },            // P001
    …
}
// nested patterns for the same variant must all be at the same payload position
```

**(c) Arms of a nesting variant must be contiguous.**

```fern
match (v) {
    P(Ok2(a)) => { return a; },
    Q(n)      => { return n; },
    P(Er2(a)) => { return 0 - a; },           // P001
    …
}
// arms for `P` with nested patterns must be contiguous
```

There is a fourth, milder one: an or-pattern alternative may not nest
(`or-patterns (`|`) may not contain nested patterns — use separate arms`).

## 3. Why the desugar cannot be widened

`buildMergedStmtArm` collapses a contiguous run of same-variant arms into

```
V(__nest0, …) => { match (__nestPos) { <one inner arm per group member>,
                                       _ => <the outer `_` body, copied> } }
```

Consider (a) with two nested positions:

```fern
P(Ok2(a), Ok2(b)) => A,
P(Ok2(a), _)      => B,
_                 => C,
```

Both arms match at position 0 and differ at position 1. Whatever the desugar
picks as `pos`, a value like `P(Ok2(1), Er2(2))` must run **B** — it fails
arm 1 at position 1 and should fall to the *next arm*. But by the time the
inner match on `__nest1` runs, arm 2 is no longer a sibling it can fall to:
it has been merged into the same inner match, and the inner match's own
wildcard is the **outer** `_`, i.e. C. The desugar's control flow only has
"this inner arm" and "the outer fallthrough"; the notion of "the next arm of
the outer match" does not survive the merge.

Limits (b) and (c) are the same fact wearing different clothes: (b) is two
arms whose discriminating column differs, and (c) is a group that cannot be
merged because a foreign arm sits between its members and would change
order-of-trial if hoisted.

So the limits are not conservatism to be relaxed. They are the exact boundary
of what "merge into one arm plus an inner match" can express.

## 4. The semantics already exist on the tuple path

`#7043` made tuple patterns **flat**: no parse-time merge, one compound test
per arm, arms tried in source order. All three shapes §2 rejects are accepted
there, and they run correctly:

```fern
match (t) {                       // t: (In, In)
    (Ok2(a), Ok2(b)) => { return a + b; },      // two nested positions
    (Ok2(a), _)      => { return 100 + a; },    // different discriminating column
    (_, Ok2(b))      => { return 200 + b; },    // …and again
    _                => { return 0; },
}
```

| input | result | why |
| --- | --- | --- |
| `(Ok2 1, Ok2 2)` | `3` | arm 1 |
| `(Ok2 1, Er2 9)` | `101` | arm 1 fails at column 1, **falls to arm 2** |
| `(Er2 9, Ok2 2)` | `202` | arms 1–2 fail at column 0, **falls to arm 3** |
| `(Er2 9, Er2 9)` | `0` | falls to `_` |

That is the target semantics, shipping today, gated by
`conformance/cases/tuple_variant_payload_subpattern`. The arm-position path
differs from it not by design but by lowering strategy.

Worth naming precisely: the tuple path is **already a pattern matrix** — one
row per arm, one column per element — compiled by linear scan with a folded
per-row test rather than by a decision tree. It re-tests shared prefixes
(three arms discriminating on column 0 test column 0 three times). A matrix
compiler is the same representation with a better compilation strategy, and
it subsumes both paths rather than adding a third.

## 5. What a decision-tree compiler is, in Fern's terms

Rows are arms, columns are sub-terms of the scrutinee. Repeatedly:

1. Pick a column (heuristics: leftmost, or the one that discriminates most).
2. Partition rows by that column's head constructor into one **switch edge**
   per constructor, plus a default edge for rows with a wildcard there.
3. Recurse per edge on the residual matrix, with the chosen column expanded
   into the constructor's sub-columns.
4. A row that becomes all-wildcard is a leaf: run that arm's body.

Order-of-trial falls out of row order within each edge — which is precisely
the property §3 says the merge destroys. Nested patterns need no special
case: nesting is what step 3 does.

The same construction answers the questions Fern currently answers ad hoc:
a matrix with no rows at a reachable edge is a **missing case** (E030), and a
row never reachable as a leaf is a **redundant arm** (E026).

## 6. What would be deleted, not ported

This is the part that makes the trade worth stating. Each of these exists
only because the pattern compiler is a parse-time merge:

- **`ast.MatchArm.FallConsumed`** — a flag marking a trailing `_` whose body
  the desugar *copied* into a merged arm's inner match, so reachability must
  not call it unreachable. A decision tree does not copy bodies, so the arm
  is either reachable or it is not, and the flag has nothing to mark.
- **`Match.Sugar` / `MatchArm.Sub`** (#7059) and the self-host's
  `StmtMatch.sugar` (#7066) — written-form carriers added so `-fmt` could
  reprint the pattern instead of the lowering. If the pattern compiler runs
  *after* the checker on typed patterns, the AST holds the pattern as
  written and there is nothing to carry.
- **`nestedPos` + `sameFieldList` + `coversEveryValue`** (~60 lines) — all
  are feasibility tests for the merge.
- **The four `P001`s of §2** — they stop being diagnosable conditions.

Sizes, measured: native `desugarNestedStmtArms` 64, `buildMergedStmtArm` 59,
and the expression-form twins 57 + 54, plus `nestedPos` 27, `rebindStmtBody`
15, `rebindExprBody` 14, `sameFieldList` 19, `coversEveryValue` 14 — ~325
lines, before the `Sugar`/`Sub` machinery. Self-host `desugar_nested_arms` 45,
`build_merged_arm` 93, `build_tuple_match` 96, `build_struct_match` 74,
`wrap_tuple_elems` 57 — ~365 lines.

## 7. Where it should run

**Not in the parser.** The current placement is why `-fmt` needed carriers and
why the checker validates a tree the programmer did not write. A decision-tree
compiler wants types (to know a constructor set is complete, and to size
sub-term loads), so it belongs after the checker, emitting a tree the IR
lowers directly — the IR already has every primitive it needs
(`OpMatchTag`, the path-addressed loads from #7043's `tupPathStep`, and the
per-depth guarded structure from the payload-slot work).

## 8. Staging

The point of staging here is that each step is separately verifiable against
the *existing* behaviour, so the switch is never a leap.

1. **Build the matrix + tree in native, behind a flag, unused.** Compile the
   existing corpus's matches to trees and assert the tree's leaf-per-input
   agrees with today's lowering on the conformance corpus. No codegen change.
2. **Switch the TUPLE path to it first.** It is already flat and already has
   the target semantics, so this is a pure lowering swap with an oracle:
   `conformance/cases/tuple_*` must be byte-identical in behaviour, and the
   shared-prefix re-testing should measurably shrink.
3. **Switch the arm-position path.** The four `P001`s become accepts; add
   conformance cases for each of §2 (a)/(b)/(c) and the or-pattern one.
4. **Delete §6.**
5. **Mirror in the self-host.** Per `docs/NATIVE-CONVERGENCE.md` this is not
   optional — the fixpoint and the `-fmt` parity gate both span the two
   compilers.

## 9. Risks

- **The self-host mirror is the long pole**, not the native work. Its three
  desugars (`build_tuple_match`, `build_struct_match`, `desugar_nested_arms`)
  are structurally different from native's, so it is a rewrite, not a port.
- **Exhaustiveness messages will change.** E030/E026 text is pinned by golden
  diagnostic tests and by the self-host's byte-for-byte parity with native's
  messages; both compilers must change the text together.
- **The corpus does not cover what this unlocks.** Nothing in
  `conformance/cases` uses the §2 shapes today, because they do not parse —
  so step 3 must land its own cases, and step 1's oracle only proves *no
  regression*, never that the new shapes are right.
- **This is a stop-the-world change to one subsystem.** It should not be
  attempted alongside other pattern work, and it is a poor fit for a
  session that also has to keep rebasing on a busy `examples/self_host/`.

## 10. What not to do

Do not widen `nestedPos` incrementally. Accepting a second nested position
without the matrix means inventing fall-to-next-arm inside the merge, which
is the decision tree with none of its structure — and it would be built at
parse time, where the types needed to get exhaustiveness right are not
available.
