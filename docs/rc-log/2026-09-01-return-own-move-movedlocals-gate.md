# The retain with no sweep left to balance it

*2026-09-01* — native `internal/ir`; surfaced as the self-host x86 assembler
leaking one `X86Asm` per assembled instruction and OOMing on `checker.fern`
(#7931's `append-cliff-x86_64` / `fixtures-selfhost-x86-64` lanes).

## The shape

```
function outer(own p: P, k: i32): P {
    if (k % 2 == 0) { return inner(p, 20); }   // early transfer — retained
    return inner(p, 20);                       // last occurrence — claimed
}
```

Move-on-call claims an `own` param only at its textually-LAST occurrence, and
the claim sets `movedLocals[p]`, which excludes p from the exit sweep on EVERY
return. `computeReturnOwnMoves` (#6125) exists to claim the early transfers
per-site — but it gated itself off whenever `movedLocals[p]` was already set,
reasoning "move-on-call already claimed it whole-function".

On the early path that left `ownArgNeedsRetain`'s retain with nothing to
balance it: retain (+1), callee consumes one, sweep skipped — the frame's own
reference is never released. One leaked box per call, every field's rc bumped
with it, and the callee grows the leaked-shared buffers at rc>1, so its first
append copies the whole buffer. Leak and O(n²) copy from one gate.

## Why the gate was wrong

Every site `computeReturnOwnMoves` claims is a *return*, so control cannot
pass through two claimed transfers: a path through an early claimed return
exits there, and a path reaching the last occurrence never saw the early one.
The whole-function sweep exclusion is identical either way. Only the
same-NODE guard (`ownCallMoveArgs[arg]`) is needed; the name-keyed
`movedLocals` gate is deleted.

## How it surfaced, and why only after Wave E

The pre-port assembler emitted its byte sequences inline in one giant
dispatch, with almost no `a`-consuming calls off the final statement. The
Wave E port (#7893, b1218a9) factored the encoders into `own`-threaded
family/adapter functions — dozens of `if (…) { return family(a, …); }`
branches whose fallthrough also transfers — the exact gated shape, once per
instruction. Compiling `lexer.fern` went 12,794 crossings / 4.85 MB →
20,085 / 335 MB; `checker.fern` exhausted the heap arena.

A second, independent taint compounded it: `x86_gas_membytes` ended its rip
path with a bare `return a;`, so `findReturnsFreshBox` disqualified it, the
`a = x86_gas_mem_op(a, …)` reassign in every family tainted `a` out of
`freeEligible`, and both the retain machinery and this pass skip a
non-eligible param entirely — same leak, different door. Fixed in the source
by making every membytes return a construction (the idiom the analysis is
built around); the file now carries a comment saying so.

## Attribution notes (what actually worked)

- `FERN_CLIFF_REPORT` only counts copies with SPARE capacity, so a shared
  append landing exactly at capacity reads as a grow — per-mnemonic probe
  deltas alternated "clean"/"copying" on identical instructions and nearly
  every theory built on that alternation was wrong.
- What settled it: `__rc_get` on an UNTOUCHED sibling field (`a.rodata`) as a
  struct-sharedness proxy. Its rc marched upward one per instruction — a
  leak, not a copy cycle. (The checker types `__rc_get` as `u8[]`; widening
  it to any array locally is a two-line debug hack, revert before commit.)
- Micro-repros of the suspect shape stayed clean until the fallthrough was
  itself an own-call — the gate needs BOTH an early transfer and a claimed
  later one to bite.

Pinned by `rc_own_remove_test.go` case `E_early_transfer_last_also_transfers`
(24 crossings before, 0 after; all three backends). After both fixes,
`lexer.fern` is back to the pre-port 12,794 / 4.85 MB with a byte-identical
binary, and `checker.fern` compiles at the pre-port 450,503 / 253 MB (that
remainder is the known front-end baseline, not the assembler).
