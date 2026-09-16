# 2026-09-16 — the end the checker proved unreachable aborts

After the flip (#9437) and the integer-keyed maps (#9449), the corpus's
largest refusal was `value-returning body falls through`, at 47 sites, and
`unsupported match guard` sat close behind at 16. Both were pattern
matches.

## What it was

The parser desugars a nested, tuple, struct-field or literal arm pattern
into a done-flag chain of flat matches: `var __na_s = scrut; var __na_d =
false; if (!__na_d) { match (__na_s) { … } } if (!__na_d) { … }`. Every
arm of the written match returns, the flag is set beside each body, and
the chain falls through by construction. The checker's E052 reads the
chain through the match it was written as (`sugar_arm_bodies`) and
accepts the body as one that cannot reach its end; the semantic lowering
read the chain as it stands, found the end live, and refused the
function, which under the all-or-nothing rule kept the whole module on
the AST lowering.

## What changed

`semsource.complete` terminates a live end of a value-returning body with
a new terminator, `unreachable` (STerm kind 4). The plan gives it a return
step with no supply and the drops of every unit the frame still holds
(`ssaunits`), the replay verifies the ledger drains there as at a return,
liveness admits it as a terminator with no successor, and the physical
lowering emits the return step's releases and then `exit 134`, the abort a
failed bounds check takes, followed by the zero the stack shape wants and
never reaches (`ssarc.terminator`). The print shows it as `unreachable`.

A guarded arm (`Line(n) when n > 0 =>`) is produced too. The guard is read
in the arm's block after the payload bindings it may name; a true guard
branches to a body block of its own and a false one to the next arm's
test, which now lists the guard's block among its predecessors. The
bindings die on the false edge as on any other, so the units they hold
are released there. A guarded arm never counts towards totality
(`arm_variant_name` already answered "" for it) and a guarded wildcard
does not close the chain, though the language's E026 keeps a wildcard
last.

## Pinned

`TestSelfHostSSAPhysicalRC` lowers a body ending in `unreachable` with an
owned string parameter: the string is freed and `exit` is emitted.
`TestSelfHostSemanticSourceRC` runs `nested_arms` (three nested arms over
a two-payload variant, a plain one and a wildcard, every arm returning),
`guarded_pick` (a guarded `Some` before an unguarded one) and
`guarded_words` (a guard on a string payload whose false edge releases the
payload, in a loop) on arm64, x86-64, the sanitiser and wasm under the
leak check. The print golden carries `ends_unreachable`, `guarded_line`
and `guarded`.

## Traps

**The RC fixture's main is AST-lowered.** A fixture that takes an enum or
an array leaks when main passes a fresh `Pr(Ok2(1), Ok2(2))` or `["ab"]`
straight to it: the AST-lowered caller does not free the temporary it
lends to a produced callee, fifteen blocks for the five calls and two
literals here. The same fixtures compiled whole through the CLI are
leak-free. The shapes that pass scalars and build their values inside
produced code are the ones every other fixture uses, and the reason.

**`P` and `Q` are already struct names in the fixture program.** A variant
spelled the same way reaches the AST-lowered main as `call Q`, which the
AST lowering refuses without a name, and `FERN_STRICT_IR=1` on the test
process is what names the site.
