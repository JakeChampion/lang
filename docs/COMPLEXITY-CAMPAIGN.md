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
| 4 | 5 arms out of `eval_expr` (`interp.fern`, 106 → 33) | 477 | 19658 |
| 5 | `lower_call_named` 477 → 411, `lower_stmt_var` 462 → 387 | **411** | 19585 |
| 6 | `parser.fern` self-test `main` 323 → 21 | 411 | 19401 |
| 7 | `printer.fern` self-test `main` 250 → 15 | 411 | 19279 |
| 8 | `interp.fern` self-test `main` 184 → 22 | 411 | 19160 |
| 9 | 10 arms out of `call_diags` (`checker.fern`, 285 → 13) | 411 | 19085 |
| 10 | 20 op emitters out of `emit_function_via_ir_pre` (299 → 231) | 411 | 19038 |
| 11 | 19 op emitters out of `emit_function_via_ir` (285 → 221) | 411 | 18998 |
| 12 | 21 value ops out of `lift_from_ir` (`ssa_lift.fern`, 255 → 189) | 411 | 18940 |

Slice 3 is the case where the metric under-reports the win. `infer_expr_type`
went 345 forks → 36, but most of that did not vanish, it MOVED: the 358-line
`ExprCall` arm became `infer_expr_call_type`, which was still 264 until its own
two-arm inner match was split again into `infer_call_named_type` (124) and
`infer_call_method_type` (139). Net excess −38 for a 503-line function becoming
six named ones, the largest 139.

That is the honest shape of extraction at the second level: splitting a
dispatcher moves complexity into named pieces long before it reduces it. Excess
falls properly only when a piece lands UNDER the limit and its whole
contribution disappears — which is why the self-test harnesses (slices 6-8) and
the op tables (10-12) pay several times better than the dispatcher splits did.
Four of parser's twenty land under; fourteen of interp's twenty-one; eighteen
of the x86-64 emitter's twenty; twenty of the lifter's twenty-one.

Slice 5 moved the ceiling for the first time, and nothing since has: it is held
by `lower_call_named` at 411, which is now a table (see "Order of work").

The ceiling is held by `lower_call_named`, which neither slice touches.

Every slice is a pure MOVE, and the early ones were re-applied onto three
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

## Picking the gate

Every change here is a pure refactor, so each has a gate that should not move.
Which gate depends on **what code path the change is in**, and getting that
wrong is the main way to spend an hour proving nothing.

**Code the compiler contains** — `irlower.fern`, `checker.fern`, the `asm_*`
emitters — is gated by `scripts/selfhost-emit-hashes`: one sha256 per
(fixture, target), 1527 rows as of 2026-08-26, before and after. The compiler's
own types cannot catch what a mechanical move gets wrong (whole families of
values share one type, so a crossed argument type-checks cleanly and surfaces
only as wrong output), and the self-compile fixpoint is SELF-REFERENTIAL and
blind to a stable miscompile (`docs/TEST-GATES.md`). Comparing emitted bytes is
what actually bites. Costs ~28 min a side on top of two `make selfhost-cli`
builds, so compute the baseline once per base commit and batch extractions.

**Consecutive slices share a sweep.** A slice's after-file is the next slice's
before-file, so only the change side needs running — half the wall clock. This
survives the rebase-merge: the new commit has a different SHA but the same tree,
and the build is deterministic, so the binary is byte-identical to the one the
previous slice already swept. `cmp` the two to confirm before relying on it; a
mismatch means something else moved and you need a fresh baseline.

**Code behind `-ssa`** needs `selfhost-emit-hashes --ssa`. The default sweep
NEVER REACHES the SSA backends, so it would compare IR-emitter bytes — which
such a change cannot affect — and report PURE for an arbitrarily broken one.

**Code the compiler does not contain at all** cannot be gated by any
emit-hash run. `ssa_lift.fern` is the example: `fern.fern` never imports it and
nothing in the CLI calls `lift_from_ir`; its only importers are four
`ssa_lift_*_run.fern` drivers. Its gate is those drivers — compile
`-target x86-64-linux`, run both sides, compare exit codes and output.

**A self-test `main`** (`parser.fern`, `printer.fern`, `interp.fern` each carry
one) is gated by compiling and running it: it exits 0, and each case returns
its own error code on failure.

Two rules that make the choice reliable:

- **`cmp` the two compiler binaries before trusting an emit-hash run.** If they
  are byte-identical, the file is not in the compiler under test and the sweep
  proves nothing. That is how the `ssa_lift.fern` dead-gate was caught, after
  the `--ssa` variant had already been set up for it.
- **Mutate something the gate should catch, and confirm it does.** Identical
  output is also exactly what a gate that never executes the moved code
  produces. Breaking one extracted helper took the lift-coverage scan from
  66/66 functions to 29/66; a `main`-split's mutation must exit with that
  case's own code.

### The cheap check that runs first

Before spending an hour, assert the move mechanically. The strongest form is a
ROUND-TRIP RECONSTRUCTION: re-inline the extracted helpers and require the
result to reproduce the base file **byte-for-byte**. It subsumes the weaker
checks (guards identical and in order, bodies verbatim modulo indent) and it
has caught every surgery slip so far — a multi-line `if` header cut after its
first line, and a hardcoded block end that swallowed a closing brace.

That plus `fern -check` costs seconds instead of an hour.

## Shapes and techniques

The big functions are not all the same shape, and the technique follows the
shape.

Finding the shape means asking, per candidate block, what it writes, what it
reads, and whether it returns. **Strip string literals and comment tails before
any of those three scans** — not just before counting braces. The comments here
quote Fern code constantly, so an unstripped `\breturn\b` scan reports early
returns in blocks that have none, and an unstripped free-variable scan invents
parameters for locals a comment merely names. Both readings are the wrong way
round: they make a mechanically extractable block look untouchable.

**Detect writes with a word-boundary match, not a start-of-line one.** Fern packs single-statement branches onto the guard line,
so `{ struct_ty = bst; }` assigns in the middle of a line and a `^\s*(\w+) = `
scan reports the block as side-effect-free — the one reading that most reliably
sends a mechanical extraction into a compile error or, worse, a silent behaviour
change. Compute reads over the WHOLE construct including its header, too: a
guard mentions locals the body never touches.

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

### Branches sharing a small write-set → a carrier whose params shadow

`lift_from_ir` (`ssa_lift.fern`) has a 64-branch chain, and 21 of those
branches — the value-producing ops — read and rewrite EXACTLY the same four
locals: the value counter, the operand stack, the instructions built so far,
and the ok flag. Nothing else from the enclosing scope.

Four locals that always move together are a concept the code has not named.
Naming them (`LiftVals`) lets those 21 bodies move out. The trick that keeps it
a provable move: **name the helper's parameters after the locals they
replace**, so each body compiles unchanged and the round-trip reconstruction
still reproduces the file byte-for-byte. Switching the bodies to `v.nvals`-style
field access would work too, and would forfeit that proof.

The precondition is that the shared write-set is genuinely small. The same
function's five control-flow branches (`if`, `else`, `loop`, `br`, `end`) write
12 to 24 of the enclosing locals each, because block bookkeeping, the scope
stack and the edge lists all move at once. A carrier for those would be the
function's entire state — a rewrite, not a refactor. They stay.

### A self-test `main` → one function per feature under test

`parser.fern`, `printer.fern` and `interp.fern` each carried a `main` that was
not compiler logic at all but a hand-rolled harness — 700 to 1000 lines of
assertions run in sequence, each returning its own error code. These split
cleanly and pay better than anything else in the campaign: a fifth to two
thirds of the resulting functions land UNDER the limit, because a test case
is mostly setup rather than branching.

Segmenting them needs care. Cut at each case-start, then **merge a segment into
its predecessor whenever it reads a variable the predecessor declared**, to
fixpoint — that rule is a property of the code and handles all three files,
whereas a per-file regex does not. `interp.fern` carries two different case
forms and a naive cut on one of them strands 38 cases in a 331-line prologue,
silently: the result still compiles and still exits 0. Assert afterwards that
the prologue is empty, that no segment reads outside itself, and that the
segment sizes sum to the whole body.

`main` becomes a list of calls that stops at the first non-zero code, which
preserves the error-code contract exactly.

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
moved out, `lower_call_method` sits at 295, and nearly all of that is the
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

Fifteen slices in, the tree has gone from 19884 excess to 18804 and the
ceiling from 477 to 411. What is done, and what the remaining shapes are:

| Function | File | Forks | Outcome |
|---|---|---|---|
| `lower_call_method` | `irlower.fern` | 472 → 295 | guard chain, 11 families out |
| `lower_expr_dispatch` | `irlower.fern` | 356 → 18 | 15-arm match, 11 arms out |
| `infer_expr_type` | `asmcore.fern` | 345 → 36 | match-based |
| `main` | `parser.fern` | 323 → 21 | self-test harness, 20 functions |
| `emit_function_via_ir_pre` | `asm_ir.fern` | 299 → 231 | op table, 20 emitters out |
| `call_diags` | `checker.fern` | 285 → 13 | match-based, 10 arms out |
| `emit_function_via_ir` | `asm_arm64_ir.fern` | 285 → 221 | op table, 19 emitters out |
| `main` | `printer.fern` | 250 → 15 | self-test harness, 14 functions |
| `lift_from_ir` | `ssa_lift.fern` | 255 → 189 | carrier, 21 value ops out |
| `main` | `interp.fern` | 184 → 22 | self-test harness, 21 functions |
| `eval_expr` | `interp.fern` | 106 → 33 | 5 arms out |
| `lower_call_named` | `irlower.fern` | 477 → 411 | generic tail out; **the rest is a table** |
| `lower_stmt_var` | `irlower.fern` | 462 → 308 | two returning guards, then eight init-shape probes |
| `bind_var_slot` | `irlower.fern` | 213 → 114 | six reclaim-marking blocks out |
| `lower_func` | `irlower.fern` | 262 → 221 | six accumulator passes out |

**Fork count alone picks the wrong target.** The cheap, provable wins are
functions whose parts are self-contained: a match whose arms each return, an op
table whose branches each thread one accumulator, a test harness whose cases are
independent. Take those before anything needing a careful read.

What is left divides into two kinds, and neither yields to extraction:

- **The complexity IS the dispatch table.** `lower_call_named`'s remaining 411
  is 116 builtin-name intercepts whose bodies average four lines; the two IR
  emitters' remaining ~220 each are 163-166 op guards. Moving bodies out of
  these buys nothing — it is already done where it paid. Reducing them means
  moving GUARDS, which needs a way for a group to decline, and for
  `lower_call_named` that allocates on the compiler's hottest path. See
  "Fall-through" and "Not all complexity is worth removing".
- **State-threading pipelines.** `lower_stmt_var`'s fall-through guards and
  `lift_from_ir`'s control-flow branches accumulate into a dozen or more shared
  locals. A carrier works only where the shared write-set is small.

**Rank candidates by shape, not by what is left over.** Probing the four
largest remaining showed fork count says almost nothing about which is next:

- `lower_call_method` (295) — 14 blocks left, and only **three** of them always
  return. The first slice took the always-returning families; what remains are
  fall-through guards, the shape that needs a decline signal. Not mechanical.
- `lower_stmt_var` (308) — same story at its top: ~13 early-return guard blocks,
  each of which can fall through to a later one.
- `lower_stmt_match` (187) and `build_expr` (`ssa.fern`, 184) — each is ONE
  construct spanning almost the whole function (684 and 819 lines). They are the
  slice-2 shape, so check each arm for the always-returns property before
  planning anything.
- `lower_func` (262 → 221, slice 15) — **the one that was mechanical.** Its
  body is a run of `while` loops and guards accumulating into locals: six wrote
  a single local (`reclaim` ×4, `tclean`, `s`), and every loop counter was dead
  afterwards, so each moved inside its helper rather than being returned. So did
  the working lists (`snap_locals`, `lacs`, `dccs`) that one pass alone consumes.
  It declares over 100 locals, so free-variable analysis matters more here than
  anywhere else.

`lower_func` also shows where this campaign stops paying: two of its blocks write
the same 19-21 parallel per-parameter arrays (`names`, `isarr`, `isstr`,
`structty`, …). A carrier for that write-set is not a refactor of `lower_func`,
it is the admission that those arrays are one missing struct. Worth doing, but as
its own change with its own justification — not smuggled in under a complexity
slice.

A caution for whoever scans for these: **a scanner that finds extents by
counting braces is wrong on `parser.fern`, `printer.fern`, `interp.fern`,
`string.fern`, `float.fern`, `asm_ir.fern`, `asm_arm64_ir.fern` and
`irlower.fern`** — they carry string literals containing braces (the asm files
emit assembly; the others embed Fern programs as test input). Strip string
literals and comments before counting, and cross-check against the first
column-0 `}`. Two further traps in the same family: `irlower.fern` is
MIS-INDENTED, with `lower_call_named`'s 116 intercepts written at the same
indent as the `if` that contains them; and `checker.fern` is 2-space indented
where most files are 4-space, so a dedent constant carried over from the
previous slice is wrong.

The stdlib is a separate, much smaller front (ceiling 68, excess 780) with no
emit-hash oracle behind it — its gate is the ordinary test suites. Worth doing
after the self-host patterns are established, since the risk profile there is
lower and the wins are smaller.
