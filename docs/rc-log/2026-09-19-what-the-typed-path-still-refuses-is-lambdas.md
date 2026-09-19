# 2026-09-19 — what the typed path still refuses is lambdas

No code change. A census, taken because "fully onto typed IR" had a number for
the compiler's own sources (8555 of 8555 declarations) and none for anything
else. The differential's own log has been saying so for a while:

> semantic lowering produced every declaration for 284 of 512 sampled seeds (0.55)

The other 45% run as MIXED modules — some declarations lowered by the typed
path, the rest by the AST one. That ratio is the real distance left, and
nothing said what was in it.

## The census

512 fernsmith programs (`fernsmith.GenMain(0..511)`), each compiled with
`FERN_SEM_IR=1 FERN_SEM_IR_REPORT=1`, every refusal line kept. Four programs
the checker rejects outright (E042, `?` on Option in a function returning i32 —
a generator bug, not a gap) leave **508 compilable**: 284 whole, 224 mixed,
**1,054 refusals**.

| refusal | count | share |
|---|---|---|
| a function value the AST lowering defines | 387 | 36.7% |
| unresolved result type, in a lifted body | 238 | 22.6% |
| call target has no semantic contract (a lambda) | 207 | 19.6% |
| return type mismatch | 48 | 4.6% |
| verifier: function value is not an element | 40 | 3.8% |
| function address is not a closure value | 27 | 2.6% |
| unsupported record literal | 10 | 0.9% |
| everything else, 12 distinct kinds | ~97 | 9.2% |

**85% of all refusals are lambdas and function values**, and the tail after
them is not one more big thing — it is a dozen small ones: `Map` iteration (10),
condition type (7), array element type (8), unresolved array literal (3),
binding type disagreements (5).

## What it would buy

Per program rather than per refusal, which is what decides whether a module is
whole:

| | programs |
|---|---|
| whole today | 284 of 508 |
| mixed whose refusals are ALL lambda / function-value | **157** of 224 (70%) |
| whole if only that gap closed | **441 of 508 (86%)** |

So one gap is worth +157 whole modules and the remaining dozen are worth +67
between them.

## Why it is a coupling, not a missing feature

The refusal that dominates is not "the typed path cannot lower a lambda". It is
`semlower.ast_built_value`:

> is a function value `<creator>` builds, which the AST lowering defines

A lambda is lifted to a hoisted body named `__mkclo$<creator>$wrapN`, and the
CREATOR — the function whose body builds the closure box — hands the value out.
If the creator is AST-lowered, the hoisted body must be too, because the AST
lowering calls it through that box under the AST convention. The 207
`call target has no semantic contract: __lam_N` are the same edge from the
other side: a produced body calling a lambda the typed path did not produce.

**The two paths' closure-call conventions do not interoperate, so a refusal
anywhere in a closure's creator chain drags the whole chain to the AST path.**
That is why one gap accounts for 70% of mixed modules: it propagates. It also
means the fix is not "add lambdas to `semsource`" but making the closure
boundary a contract both paths can meet, which is the same shape as the
direct-call contracts (`ssarc.caller_sigs`, `irlower.consume_sigs`) that
already exist for ordinary calls.

## Trap

The compiler's own sources are 8555 of 8555, and that number is worth nothing
as a coverage claim. It says the typed path handles the code this project
happens to write, in a tree with a house style — and the house style barely
uses lambdas where a random program uses them constantly. **A self-hosting
compiler measuring itself measures its own idiom.** The fuzzer's 55% is the
honest figure, and it was in the differential's log the whole time, logged and
unread.
