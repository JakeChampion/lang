# 2026-10-02 — a shared flag row does not pay for its threading

`ssaunits.carried_params`. Refs #8171. Measured and not landed.

`2026-10-02-p-the-relaxation-pass-keeps-its-state-in-locals.md` named a
lead: `carried_params` allocates a flag per value of the function for each
call, and sharing one cleared row across a function's calls "is the shape that
would keep the gain". That shape needed #11093's E051 fix to be written, and,
once written, measured:

| | main (bca4b4c) | shared row |
|---|--:|--:|
| stage 2, total Ir | 31.04 G | 31.02 G (−0.06%) |
| `ssaunits.bits` self Ir | 115.7 M | 42.9 M |
| `ssaunits.grow_rows` self Ir | 51.7 M | 61.3 M |
| `ssaunits.carried_params` self Ir | 22.8 M | 29.2 M |
| `ssaunits.dying_lent_rows` self Ir | 19.2 M | 23.3 M |

The allocation it removes is mostly paid back by threading the row in and out
of `dying_lent_rows` and `carried_params` (the `Lent` and `Carried` results)
and by clearing each walk's flags. Using it in the compiler's own sources
would also have needed a fresh stage0 pin, since the pinned compiler predates
#11093. Neither was worth it for 0.06%, so the change was dropped.
