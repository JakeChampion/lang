# 2026-09-18 — the donor that dies at its own construction

**`2026-09-18-the-in-block-pairing-is-close-to-exhausted.md` is wrong**, and
this entry is the correction. That one priced three leads at +1, +6 and +31,
measured 4,129 constructions with no donor above them, and concluded that the
binding constraint was slot-count coincidence rather than a missing form. The
measurement was right; the conclusion was not, because every probe in it asked
the same question — which constructions have a donor dying at an EARLIER
position — and never asked about a donor dying at the construction's own.

**795 constructions have a matching-slot value dying at their own instruction
and are not paired.** Against 390 pairings today and a 144 cross-block ceiling.

## What the shape is

An update chain — `a = X86Asm { ...a, code: … }`, the assemblers' idiom — reads
the fields it carries over, builds the new box, and the superseded box dies
there. Its drop lands at the construction's own step, not before it, so
`claimed_after` sees no claim at a LATER position and never takes it as a
donor. The value is not an operand of the construction either (the projections
that read it are earlier instructions), so nothing about it is unsafe to hand
on; it is simply invisible to a pairing that only looks backwards.

This is the AST lowering's `emit_self_overwrite_reuse`, and it is where that
path's remaining advantage lives. Per-function comparison of the two paths'
`__fern_alloc_reuse` sites on the compiler's own sources:

| | sites |
|---|---|
| AST reuses where the typed path does not | 348, across 126 functions |
| of which `arm64_native` | 156 |
| of which `x86_native` | 147 |
| typed reuses where the AST path does not | 289, across 108 functions |
| of which `astwalk` / `parser` / `irlower` | 205 |

The two paths reuse in largely different places: the typed one is ahead on the
tree walkers and behind on the assemblers, and the assemblers are nothing but
update chains.

## Why the earlier entry reached the wrong conclusion

It ranked the forms it had already conceived of. `variant_new`, `array_new` and
cross-block were the three the previous work left on the table, so those were
the three measured; the one that mattered was not on the list, so no probe
addressed it. Reading the 4,129 as "donors and constructions rarely agree on
size" was reading a number produced by a question with a hidden premise —
"donor" meaning "value dropped at an earlier position" throughout.

The trap generalises, and it is the fifth time in this series that a ranking
has pointed the wrong way: **a probe measures the hypotheses it was written
for.** The 4,129 is still true and still says what it says about earlier-drop
donors. It says nothing about same-instruction ones, and the entry that quoted
it did not notice the difference.

## What it cost to take

Structurally the smallest of the forms, not the largest: donor and recipient
are one instruction, in one block, with no token carried across control flow
and no slot-conflict analysis. `ReusePairs` gains `selfs[i]`, `block_body`
emits that token BEFORE the construction where an earlier drop's is already in
the slot, and `drops_less` skips the box the construction took.

**Measured, compiling the compiler to x86-64:**

| pairing | firings | emitted lines |
|---|---|---|
| before this entry | 390 | 3,506,632 |
| **with the same-instruction form** | **1,167** | **3,544,008** |
| the AST lowering, same sources | 449 | 2,761,415 |

+777 pairings, and the typed path is now 2.6x the AST one where it was 0.87x.
The predicted 795 and the delivered 796 agree; the 19 between 796 and 777 is
the refill rule, removed below.

## What came out: the refill rule

`2026-09-18-hold-the-donor-for-the-construction-that-wants-it.md` added a
second reading — the slot a construction frees by spending its token can be
refilled by a value dying at that same instruction — and witnessed it with
`chain_up`, worth 44 of that entry's 103.

**The same-instruction pairing subsumes its witness.** `chain_up` fires 3 with
the refill rule reverted, because every box it was refilling with is now spent
on the spot instead of saved. Measured over the whole compiler the refill is
worth **+19 of 1,186**, and no fixture distinguishes it: two attempts at one
failed, because in straight-line code a value's last use is a projection, so
the only value dying AT a construction is the box that construction supersedes
— which matches its arity and is taken by the new rule first. Its real sites
(47, in `parser.mono_expr`, the `astwalk` folds and `irlower`) are nested
constructions of differing arity, which no small fixture reproduces.

So it is removed rather than carried unwitnessed: 1.6% for a rule no test can
fail on, in code where a wrong pairing is a double free, is the wrong side of
that trade. The drop scan goes back into an `else`, which is also what it
meant in the first place.

## The soundness condition, found in review

The first cut took a self donor whenever no EARLIER donor served the
construction — and the pending donor is still held in that case, waiting for a
construction further down. **The frame has one token slot.** A self pairing
emitted while a donor is held overwrites that donor's box with its own, and the
later construction is handed a box the self-update has already built into and
is returning live.

```fern
var a: A3 = A3 { p: "aa", q: seed };      // 3-slot donor, held pending
var s: i32 = a.q + a.p.len();             // a dies here
var b: B4 = B4 { x: "bb", y: seed, z: seed + 1 };
b = B4 { ...b, y: s };                    // 4-slot self pairing — clobbers the slot
var c: A3 = A3 { p: "cc", q: b.y + b.z }; // reads b's box, not a's
```

It reads as a LEAK — `allocs=3 frees=3 live_bytes=24` where the same program
balances with the pairing off — because the two boxes differ in size class, so
`__fern_alloc_reuse` declines and puts a LIVE block on its freelist. Where the
sizes agree it is an alias instead, which is worse and quieter.

So the condition is `pending < 0 && claims >= 0`, and it is soundness rather
than preference. **It costs nothing measurable**: the whole compiler emits
1,167 pairings with the guard and 1,167 without it, because no function in the
tree has a held donor straddling a self-update of a different arity. Which is
exactly why every suite passed while it was wrong — the differential, the RC
suite, the SSA agreement, the lint ratchet and the whole-compiler fixpoint,
all green on a pairing that aliases two live values.

Found by a review bot on the PR, with a fixture; confirmed by running it
rather than on its word, and its fix taken unchanged. `token-slot-overlap` in
`TestSelfHostSemanticReuseDifferentialX86_64` is that fixture.

## Trap

The entry this one corrects ranked the forms it had conceived of and read the
result as a property of the program. The same care applies to what replaced
it: `chain_up` was a valid witness when it landed and stopped being one when a
stronger rule arrived, silently, with the suite still green. **A witness is
only a witness against the rules that existed when it was written** — which is
why each half of `pairing-reach` is re-verified by reverting its own rule
whenever that file changes.

The second trap is the sharper one. Every gate this project holds a reuse
change behind passed on a pairing that hands a live box to a second value,
because the compiler's own sources do not contain the shape. **A suite that
compiles this tree cannot witness a rule this tree does not exercise**, and the
whole-compiler fixpoint is the least able of them to: it proves the compiler
reproduces itself, and a pairing that never fires there is invisible to it. A
new pairing rule needs a fixture built for the rule, and the fixture has to be
shown to FAIL without it.
