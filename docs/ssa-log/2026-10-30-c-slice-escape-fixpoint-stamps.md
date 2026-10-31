# 2026-10-30 — the slice-escape fixpoint walks only stale callers

`checker` slice-escape summaries: `slc_stale`, `slc_callee_order` and
the fixpoint in the module check. Refs #8171.

## The shape

The checker finds which parameters each function can return as a view.
A fixpoint walks each function and updates its summary. In each round,
the old fixpoint walked every caller of each function whose summary
changed in the round before. It did this also when the caller was
walked after that change. The fixpoint made 40935 walks for about 15800
functions in one compile of the compiler. That was 0.84% of the compile.

## What changed

Each walk and each summary change now gets a stamp from one counter. The
fixpoint walks a function again only when a callee summary is newer
than the last walk.

The fixpoint also walks the functions in callee-first order. A
depth-first search over the call names gives this order. Thus most
callees have their final summary before the first walk of their
callers.

A summary only gets more flags. Thus the order of the walks does not
change the result of the fixpoint.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is the replay index
slice of 2026-10-30-b on main at 39d1bc8d0.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 100.407 G | 100.215 G (−0.19%) |

The change stamps alone gave −0.10%, and the callee-first order gave
the remaining −0.09%. The whole-compiler binary is byte-identical to the
base compiler's.
