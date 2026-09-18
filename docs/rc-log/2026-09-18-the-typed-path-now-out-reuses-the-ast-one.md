# 2026-09-18 — the typed path now out-reuses the AST one

No code change. This entry is the measurement taken after
`the-donor-that-dies-at-its-own-construction.md` landed, and it closes the
in-block pairing question that entry re-opened — this time with the number the
earlier probes could not see.

## Where the two paths stand

Compiling the compiler's own sources to x86-64, counting
`call __fn___fern_alloc_reuse` sites per emitted function:

| | sites |
|---|---|
| typed path, total | 1,167 |
| AST lowering, total | 449 |
| functions where the AST path reuses and the typed one does not | **8**, one site each |
| functions where the typed path reuses and the AST one does not | 245, 726 sites |

The 348-site, 126-function AST advantage that
`the-donor-that-dies-at-its-own-construction.md` measured is **8 sites**. All
eight are peephole rules in the two native assemblers — `asm_ir.peep_p1`,
`peep_p4`, `peep_p5_acc`, `peep_p5_call` and their arm64 twins.

The AST column is a count of what that lowering EMITS, not a reference to
measure correctness against: a compiler built through it aborts on a bounds
check compiling half these modules (#9763, and
`cross-block-is-71-and-the-real-cost-is-elsewhere.md`). Reuse-site counts are
still comparable — the emit itself succeeds — but nothing else in a diff
against that path should be read as a target.

Separately: `FERN_SEM_IR_REPORT=1` says **produced 8555 of 8555 declarations**.
The typed path refuses nothing in the compiler's own sources, so the AST
lowering is dead code for this tree — it is reached only by whatever a future
program uses that the typed path declines.

## What the last 8 are

Not a construction form. A donor whose box is still live at the construction
because something was projected out of it and not retained:

```fern
var t: string = p.text;          // borrowed out of p, no inc
p = Peep { ...p, n: n - 2 };     // p's box would be the recipient
if (t.len() == 7) { return p; }  // t still read here
```

The plan keeps `p` alive as the owner of `t`, so `p` is dropped after the
construction rather than at it, and every pairing rule requires the donor to be
dead there — correctly, since reusing a live box and then dropping it is a
double free. The AST lowering reuses here because its eligibility test is
syntactic: `t` is a `var` it treats as carrying its own count.

Reduced to a 20-line fixture the shape is exact — the typed path fires 0 and
the AST path 1 — and it is the projection that decides. Reading an `i32`
through the same field fires on both, because a scalar projection borrows
nothing.

## The scale of it, which is small

A probe in `reuse_pairs` (not committed) over every unpaired construction with
a slot count, classifying by whether a matching-slot value is dropped later in
the same block and whether it is read at or after the construction:

| | sites |
|---|---|
| a matching-slot donor dies later, and IS read after the construction | 613 |
| a matching-slot donor dies later, and is NOT read after it | **32** |
| no matching-slot donor dies anywhere in this block | 3,091 |
| a matching-slot donor dies here but is an operand of the construction | **0** |

So the borrow-defers-the-drop shape is worth **32** in the whole compiler, not
the 645 the coarser first cut suggested — the 613 are donors that are genuinely
read after the construction, and no rule may take those. Buying the 32 means
retaining on projection so the donor can die earlier: an inc and a dec on every
borrowed child against one allocation saved on a fraction of them. That is a
change to the unit planner's borrow inference, not to `ssarc`, and 32 does not
pay for it.

**The operand exclusion has never rejected anything.** It stays — it is the
reason "the construction does not read the donor" is true rather than assumed —
but it costs nothing and buys nothing, and nobody should go looking for yield
there.

## What is actually left

3,091, the population no in-block rule can reach. That is the cross-block lead
priced at +144 in `the-in-block-pairing-is-close-to-exhausted.md`, whose
mechanism (a token in a frame slot, threaded across arbitrary control flow,
proved unclobbered on every path and unspent on every back edge) has not got
cheaper. In-block is now exhausted for real, and this time the claim comes with
the number for the shape the last probe could not ask about.

## Trap

The gate run this entry's measurements ran beside was invalidated by the
measuring. `TestSelfHostSemanticWholeCompilerX86_64` builds the driver from the
working tree and then has it build itself; a scratch probe added to
`ssarc.fern` while it ran put the two generations on different sources, and it
failed by 8,910 bytes. That is not the one-byte intermittent of #9737 and
should not be read as one — **any tree edit during that gate makes its result
meaningless**. Probe builds belong in a copy.
