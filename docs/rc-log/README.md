# RC / Perceus self-host port — implementation log

One file per landed change. Name it `YYYY-MM-DD-<slug>.md`, write it once, and
leave it alone afterwards; the filename sorts it, so nothing needs an index.

This is the continuation of `../RC-PERCEUS-SELF-HOST-PORT.md` §9, which holds
the 291 entries up to 2026-08-20 and is closed to new ones.

## Why the split

§9 reached 10,938 lines — 95% of its file — and every entry appends to the same
tail. Two goal-2 PRs in flight at once therefore conflict *by construction*: git
cannot merge two appends at one anchor, however unrelated their content. That is
not a hypothetical. On 2026-08-19 one three-line fix (#7189) needed **four**
rebases, each resolving the identical doc collision, while main landed ~15
commits around it. The compiler change never conflicted once.

A directory has no shared anchor, so two entries landing together do not touch
the same bytes.

## What an entry is for

The same thing §9 was for, unchanged: what the shape measured before and after,
on which backends, which guard is *witnessed* versus contract-only, and what the
next lead is. Prefer the measurement to the narrative — a number someone can
re-derive beats a paragraph about how it was found, unless how it was found is
the finding.

Record the traps too. Several §9 entries exist only because a null result was
misread, and saying so is what stops the next person repeating it.

## Reading the log

```
ls docs/rc-log/            # chronological, by filename
grep -rl '<shape>' docs/rc-log/ ../RC-PERCEUS-SELF-HOST-PORT.md
```

Both halves are prose; grep spans them.
