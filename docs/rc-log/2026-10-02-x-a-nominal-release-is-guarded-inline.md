# 2026-10-02 — a nominal release is guarded inline

`asm_ir.ssa_release_nominal`, `asm_ir.ssa_reg_call_emit`, and their twins
in `asm_arm64_ir`. Refs #8171. Emitted bytes change: of the 1,965 rows
of the `selfhost-emit-hashes` sweep, 600 differ from a compiler built
from main at 173f70a4 (every program that releases a record or union),
with the same 1,708 emitted and 257 refused.

## What the profile named

The `__sem_release_<T>` helpers were 2.25 G of self cost on the 31.12 G
stage-2 compile of `checker.fern`, 7.2%, from about 70 M calls:
`typeinfo.Type` 20 M (494 M), `ast.Expr` 17.6 M (413 M), `ast.Stmt`
8.4 M, `ir.Op` 8.1 M, `ssa.SInst` 5.8 M, `lexer.Token` 2.6 M, … Only
1.3 M of the `Type` calls and 1.2 M of the `Expr` calls freed anything
(`__sem_drop_<T>`); the rest found a count above one, or a static box,
and came back having decremented or done nothing, through a call, a
frame, and the helper's own tests. An array's release
(`__fern_rc_dec`) already had that fast path inline at the call site
(`ssa_rc_dec`, 2026-09-27); a nominal release, since it became one call
(`2026-09-27-a-nominal-release-is-one-call.md`), had none.

## What changed

`ssa_call` routes a direct call to `__sem_release_<T>` with one argument
and a register entry through `ssa_release_nominal`, which renders the
same tests `ssa_rc_dec` renders: a value below the heap floor skips, a
count above one is decremented in place, a negative count (a static
box) skips, and a count of one or zero calls the helper as before. The
helper answers 0; the inline arms zero the result only when it has a
home. `rc_free_debug` keeps the plain call, as it does for arrays. The
register-call emission the arm shares with every other direct call is
`ssa_reg_call_emit`. arm64 is the same shape (`cmp … lsl #12`, `ldur`,
`tbnz w5, #31`).

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 173f70a4 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 31.12 G | 30.34 G (−2.53%) |
| stage 2, every `__sem_release_<T>` self Ir | 2.25 G | 427 M |
| stage 2, `__sem_release_typeinfo__Type` self Ir | 494 M | 38 M |
| stage 2, `__sem_release_ast__Expr` self Ir | 413 M | 34 M |
| stage 2, `x86_native.*` self Ir (the assembler) | 3.06 G | 3.48 G |
| `checker.fern` emitted text, lines | 758,038 | 848,534 |

The guard is eight instructions and two labels at each of the 7,389
release sites of `checker.fern` where one call stood, so the text grows
12% and the assembler's first round pays for it: 419 M of the 1.83 G
the releases gave back. The first version of the guard, without the
static-box test, measured 30.52 G: 2 M of the remaining helper calls
were for static types.

## Witnessed

`TestSelfHostRc*`, `TestSelfHostLeakMatrixX86_64`, `TestSelfHostSSA*`,
`TestSelfHostSemantic*`, `TestSelfHostUAF*`,
`TestFernFixturesSelfHostX86_64` (`FERN_SELFHOST_FIXTURES=1`), the lint
ratchet, `make fmt-check`, and the emit-hash sweep's counts. The arm64
and wasm lanes, the fixpoint and the differential suites are CI's.

## Next

The text the guard adds is 97% repeats (`cmpq $0x10000, %r11`, `cmpl
$1, -8(%r11)`, `subl $1, -8(%r11)`), which the line memo of
`2026-10-02-w` parses once; the three branches per site name labels and
stay unmemoised. `__sem_release_typeinfo__Type` is still called 391k
times from `checker.check_call_expr` through the stack path, not the
register path: a call `ssa_reg_call` refuses, which is where the next
calls of this kind are.
