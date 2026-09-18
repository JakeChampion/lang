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

## What it costs to take

Structurally the smallest of the forms, not the largest: donor and recipient
are one instruction, in one block, with no token carried across control flow
and no slot-conflict analysis. What moves is the emission ORDER —
`block_body` emits `reuse_construct` then `reuse_token`, and this shape needs
the token taken from the dying value before the construction builds through
it — plus `drops_less` skipping the value the construction took, which it
already does for the paired case.

Unmeasured, and to be measured before building: how many of the 795 survive
the uniqueness test at run time. A self-overwrite whose box is shared degrades
to a fresh allocation exactly as any other pairing does, so the ceiling is
sound but the yield is not yet known. The allocation-saved assertions in
`TestSelfHostSemanticReuseDifferentialX86_64` are what will say.
