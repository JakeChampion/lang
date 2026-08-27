# Reducing complexity in the first-party Fern sources

`fern -lint` measures cyclomatic complexity and `internal/lint/repo_gate_test.go`
ratchets this repository's own Fern sources against it (`docs/LINT.md`). This
file is the campaign that number tracks: what has been done, what is next, and
the two things that make the work safe rather than brave.

Starting point, measured 2026-08-26 at a limit of 10:

| Tree | Ceiling | Total excess |
|---|---|---|
| `examples/self_host` | 472 | 19847 |
| `internal/stdlib/std` | 68 | 780 |

Those exact numbers no longer hold: main lands self-host changes several times
an hour, which is why the gate carries a tolerance (`docs/LINT.md`). Read the
table below as slice-over-slice deltas against the base each was measured on,
not as a running total.

Progress:

| Slice | Change | Ceiling | Excess |
|---|---|---|---|
| — | base (`81af9ae`) | 477 | 19884 |
| 1 | 11 families out of `lower_call_method` (472 → 317) | 477 | — |
| 2 | 11 arms out of `lower_expr_dispatch` (1692 lines → 38) | 477 | 19732 |
| 3 | `infer_expr_type` in `asmcore.fern` (503 lines → 76) | 477 | 19694 |

Slice 3 is the case where the metric under-reports the win. `infer_expr_type`
went 345 forks → 36, but most of that did not vanish, it MOVED: the 358-line
`ExprCall` arm became `infer_expr_call_type`, which was still 264 until its own
two-arm inner match was split again into `infer_call_named_type` (124) and
`infer_call_method_type` (139). Net excess −38 for a 503-line function becoming
six named ones, the largest 139.

That is the honest shape of this work at the second level: splitting a
dispatcher moves complexity into named pieces long before it reduces it. The
excess only really falls once the leaves get simpler, which is a different and
slower job than extraction.

The ceiling is held by `lower_call_named`, which neither slice touches.

Both slices are pure MOVES, and both have been re-applied onto three
successive bases as main advanced under them — `fce242c`, `1a3890d`,
`c0d4648`, `5ada65a`, `81af9ae`. That is cheap only because the extraction is a SCRIPT that finds
its guards and arms BY TEXT: it relocates them wherever they have drifted to,
and main's edits inside a moved region ride into the extracted helper without
anyone hand-resolving a conflict marker in a 54,000-line file. Prefer
re-applying to rebasing for any pure move of this size.

Each re-application is re-verified the same way before anything expensive
runs: every guard and arm byte-identical and in order, every extracted body
verbatim in the original modulo indent.

The last of those bases carried #6993, which moved the AST types from
`parser.` to `ast.` and rewrote 5,900 lines of this file. The script relocated
every guard and arm anyway — they are matched on their own text, and the type
prefix only appears in the signatures it generates, so retargeting it was one
constant. A rebase would have been 5,900 lines of conflict.

**Emit-hash verified pure on `c0d4648`**: 1527 (fixture, target) pairs,
byte-identical. Do not chase a proof against whatever main is right now —
main touches this file about every 40 minutes and a purity cycle takes about
70, so "current" is unreachable by construction. The gate proves a property
of the TRANSFORMATION, not of the base, and it has now returned PURE on every
base it was run against.

A side effect worth noticing but not chasing: the self-host binary shrank
7.6% (39.6 MB → 36.6 MB) across the two slices.

## The oracle: `scripts/selfhost-emit-hashes`

Every change here is a PURE REFACTOR of the compiler, so it has a perfect
test: the bytes it emits for the whole fixture corpus, on all three backends,
must not move. `scripts/selfhost-emit-hashes` produces one sha256 per
(fixture, target) — 1527 rows as of 2026-08-26 — and a refactor is pure iff
the before and after files are identical.

Nothing else here is sufficient. The self-compile fixpoint is
SELF-REFERENTIAL and structurally blind to a stable miscompile
(`docs/TEST-GATES.md`), and the compiler's own types cannot catch what a
mechanical move gets wrong: whole families of values share one type, so a
crossed argument type-checks cleanly and surfaces only as wrong output.

**The run costs ~28 minutes**, before and after, on top of two
`make selfhost-cli` builds. So the loop is:

```
make selfhost-cli && cp bin/fern-selfhost /tmp/before
scripts/selfhost-emit-hashes /tmp/before /tmp/hashes-before.txt   # ~28 min
   ...several extractions...
make selfhost-cli
scripts/selfhost-emit-hashes bin/fern-selfhost /tmp/hashes-after.txt
diff /tmp/hashes-before.txt /tmp/hashes-after.txt && echo PURE
```

Compute the baseline ONCE per base commit and batch several extractions into
one after-run. Two extractions cost the same wall-clock as twelve.

### The cheap check that runs first

Before spending an hour, assert the move mechanically — it catches everything
a slip in the surgery would cause:

- every top-level guard in the parent is byte-identical and in the same order;
- every extracted body appears verbatim in the original, modulo one indent.

That plus `fern -check` caught a real mis-split (a multi-line `if` header cut
after its first line) in seconds rather than after a 28-minute run.

## Two shapes, two techniques

The big functions are not all the same shape, and the technique follows the
shape.

### A flat guard chain → move the body, keep the guard

`lower_call_method` was the archetype: 1752 lines, 472 forks, a sequence of
self-contained `if (…) { … return … }` blocks ending in `s.fail()`. Each
family's BODY moves to a named function; its GUARD stays exactly where it was.
Dispatch order is untouched, which is what makes it reviewable as a move.

Two preconditions, both checkable before editing:

- **The block must always return.** A block that can fall out of its `if` to a
  later guard needs a handled/not-handled signal, which a plain `LowerState`
  return cannot carry — `ok == false` already means "failed while lowering",
  and conflating the two would let a failed lowering fall through and match a
  later family. See "Fall-through" below.
- **Its free variables must be known.** Anything the body reads that was
  computed before the guard (`mtype`, `rty`, `pty`) becomes a parameter.

### A big `match` → move each arm's body

`lower_expr_dispatch` is the archetype: 1660 lines, 15 arms, the largest
(`ExprStructLit`) 522 lines on its own. Same move, same preconditions — an arm
that falls off its end continues after the match, so it needs the same
always-returns check.

**Done — slice 2.** It passed that check almost perfectly: **14 of its 15 arms
end in a return**,
and the fifteenth is the one-line `_ => { return s.fail(); }`. Each arm binds
exactly one variable — its pattern binding — so every helper signature is
`(binding, s: LowerState): LowerState` with no free-variable analysis needed
at all.

| Arm | Lines |
|---|---|
| `ExprStructLit` | 522 |
| `ExprBinary` | 280 |
| `ExprCall` | 202 |
| `ExprFieldAccess` | 149 |
| `ExprUnary` | 128 |
| `ExprArray` | 90 |
| `ExprIndex` | 83 |
| `ExprTuple` | 73 |
| `ExprSlice` | 48 |
| `ExprIdent` / `ExprLambda` / `ExprNumber` / `ExprBool` / `ExprString` | 32 / 26 / 10 / 5 / 5 |

Eleven arms moved out (the three scalar-literal arms, 5-10 lines each, stayed
inline — a function per arm is the point only where an arm has a body worth
naming). The dispatcher went from 1660 lines to 38.

One correction the type-checker made immediately: two arms read the match
SCRUTINEE, not just their own binding, so they take it as a second parameter.
The first attempt did not pass it and `fern -check` named both sites in
seconds. Do this against a compiler that type-checks the result, not by hand.

## Fall-through, and why some blocks are still inline

Several families' blocks CAN fall through: the guard matches, an inner
condition does not, and control continues to a later guard. Extracting one
means the helper has to say "not mine", and the honest way to spell that is
`Option[LowerState]`, matched at the call site.

That is correct but not free: it boxes an Option per lowered call on the
compiler's hottest path, and nothing in CI gates the compiler's own compile
TIME (`docs/TEST-GATES.md`). Worth doing deliberately, with a measurement, and
not as a side effect of a complexity refactor. Until then those families stay
inline.

## Not all complexity is worth removing

A flat dispatch table's fork count IS its entry count. After eleven families
moved out, `lower_call_method` sits at 317, and nearly all of that is the
guards themselves — one per method the language dispatches. Splitting the
table further would not make it more readable, it would just spread the same
list over more functions.

When a function reaches that state, the honest end is an
`// fern-lint: allow cyclomatic-complexity` on it with a line saying it is a
table. That drops it out of the tree's excess. It does NOT drop out of the
ceiling, by design — so the ceiling stops being a campaign metric once the
remaining monsters are all legitimate tables, and excess becomes the number
that matters. Do not annotate a function until its bodies really have moved
out; the annotation is a claim about shape, and a 1752-line `if` chain is not
a table.

## Order of work

Ranked by fork count, with the shape that decides the technique:

| Function | File | Forks | Shape |
|---|---|---|---|
| ~~`lower_call_method`~~ | `irlower.fern` | ~~472~~ → 317 | guard chain — **done**, 11 families out |
| `lower_call_named` | `irlower.fern` | 468 | deeply nested; only 3 top-level ifs — needs reading, not a mechanical move |
| `lower_stmt_var` | `irlower.fern` | 462 | guard chain, but see below — only ~6 of 20 blocks are mechanically safe |
| ~~`lower_expr_dispatch`~~ | `irlower.fern` | ~~356~~ → 18 | 15-arm match — **done**, 11 arms out |
| `infer_expr_type` | `asmcore.fern` | 345 | match-based |
| `main` | `parser.fern` | 323 | — |
| `emit_function_via_ir_pre` | `asm_ir.fern` | 302 | — |
| `emit_function_via_ir` | `asm_arm64_ir.fern` | 288 | — |
| `call_diags` | `checker.fern` | 285 | match-based |

**`lower_expr_dispatch` went first**, not the two functions above it. Fork count
alone picks the wrong target: `lower_stmt_var` has the same guard-chain shape
as the function already done, but most of its blocks FALL THROUGH *and* thread
mutable outer state — `se`, `struct_ty`, `opt_ty`, `is_arr`, `alias_inc`,
`arrarr_e`, `is_arrarr` are reassigned across block boundaries, so a helper
would have to hand a bundle of updated state back and the move stops being
mechanical. Only about six of its twenty blocks (the `comma >= 0` destructuring
arm at 180 lines, the `clo_init` one at 69, and four in the 20-30 range) meet
the always-returns precondition.

`lower_expr_dispatch` was 106 forks smaller and far safer: every arm
self-contained, every substantial one returning, no shared mutable state
between arms. Take the cheap, provable wins before the ones that need a
careful read — and leave `lower_call_named` and the state-threading half of
`lower_stmt_var` for a change that is allowed to be a rewrite, with its own
reasoning and its own review.

By the same rule the next mechanical candidates are the remaining big match
arms, not the remaining big fork counts. `infer_expr_type` (`asmcore.fern`,
345 forks) has four arms of 40+ lines that all return; `build_expr`
(`ssa.fern`) and `eval_expr` (`interp.fern`) have five each, four returning.

A caution for whoever scans for them: a scanner that finds function extents by
counting braces is WRONG on `parser.fern`, `string.fern` and `float.fern`,
which carry very long string literals containing braces. Ranking those files
by a brace-counted scan produced an obviously impossible number (19063 arm
lines in one function) — believe the type-checker and the gate, not the
scanner.

The stdlib is a separate, much smaller front (ceiling 68, excess 780) with no
emit-hash oracle behind it — its gate is the ordinary test suites. Worth doing
after the self-host patterns are established, since the risk profile there is
lower and the wins are smaller.
