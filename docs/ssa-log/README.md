# SSA backends — measurement log

One file per landed slice. Name it `YYYY-MM-DD-<slug>.md`, write it once, and
leave it alone afterwards; the filename sorts it, so nothing needs an index.

`../SSA-DECISION.md` holds the standing decision, the tripwires and the
per-backend disposition, and closed to dated measurement sections on
2026-09-16: every slice was appending its "Measured" section at the same
anchor, so two SSA PRs in flight conflicted by construction, exactly as the
rc log describes for its predecessor (`../rc-log/README.md`). A directory
has no shared anchor.

## What an entry is for

What the slice changed, the shape it was chasing, and the numbers before and
after — best of five, which target, native or under qemu, and against which
main. Prefer the measurement to the narrative; a number someone can re-derive
beats a paragraph about how it was found, unless how it was found is the
finding. Record the traps too.

## Reading the log

```
ls docs/ssa-log/            # chronological, by filename
grep -rl "<bench>" docs/ssa-log/ docs/SSA-DECISION.md
```
