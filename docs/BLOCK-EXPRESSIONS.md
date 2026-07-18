# Block-expressions

A **block-expression** is a brace-delimited sequence of statements
followed by an optional trailing value expression:

```
{ stmt; stmt; …; tailExpr }
```

The statements run first, in a fresh child scope. The **trailing
expression — written WITHOUT a `;`** — is the block's value. A block
whose final element is a `;`-terminated statement has **no value** (its
type is `void`).

This is the Rust/Zig-style "everything is an expression" block, adapted
to Fern's grammar. It enables multi-statement value-position branches:

```
var x: i32 = if (e > 0) { var k = e + 1; k } else { 0 };

var label: string = match (tag) {
    0 => { var s = lookup(tag); s },
    _ => "other"
};
```

## Why a new grammar form (not a desugar)

Two facts forced a dedicated construct rather than reusing an existing
shape:

1. **Fern lambdas do not infer return types.** An IIFE desugar
   (`(function() { … })()`) can't work, because the synthesised lambda
   would need a return type the compiler can't infer.
2. **Fern requires a `;` on every statement.** A trailing expression
   *without* a `;` is therefore a genuinely new grammar form — there was
   no existing place where the final element of a brace block could be a
   bare value.

So the block-expression is its own AST node (`ast.BlockExpr{Stmts, Tail}`),
not sugar over a function call or a desugar to anything else.

## Scope

Where block-expressions may appear (unchanged since slice 1):

- **Only `if` / `match` *expression* branches** parse a block-with-tail.
  Concretely: the `{ … }` after `if (cond)`, after `else`, and after a
  `match`-arm `=>` may be a `{ stmts; tail }` block.
- A branch that is a **single expression with no leading statements**
  stays a bare expression (`{ 1 }` is the value `1`, not a `BlockExpr`),
  so existing single-expression `if`/`match` expressions are unchanged.
- **Statement-position `if`/`match`** (function bodies, loop bodies,
  general statement blocks) are **untouched**. Only the if/match
  *expression*-branch parse path changed.
- **General value-position `{ … }`** elsewhere is NOT a block-expression
  yet (it collides with struct-literal syntax — a later slice).

Backend support:

- **Interpreter** (slice 1). The native interpreter (`fern -interp`)
  evaluates block-expressions: child env, exec statements, eval tail.
- **Compiled backends** (slice 2, `#4405`). wasm / arm64 / x86-64 all
  **lower `BlockExpr`** through the target-agnostic IR — no new IR op:
  the leading statements lower through the normal statement path, then
  `Tail` lowers as the block's result value on the operand stack (see
  the `*ast.BlockExpr` arm in `(*ir.builder).expr`). Block-locals get
  their own zero-init'd slots (shadowrename gives the block its own
  frame) and are dropped by the ordinary function-exit dec sweep, so a
  heap-valued tail flows out correctly under RC. Differential coverage
  across all three backends: `internal/e2e/block_expr_test.go`
  (`TestBlockExprCompiled*`).
- **Self-hosted compiler** (slice 2, `#4405`). The self-host parser
  parses each if/match value branch as a block-with-tail
  (`parse_branch_body` → leading `;`-terminated statements then a
  trailing tail written WITHOUT a `;`), producing a `Stmt[]` ending in
  `s_return(tail)` that irlower's `lower_value_tail` lowers. A lone
  trailing expression with no leading statements stays `[s_return(expr)]`,
  byte-identical to the single-expr branch. Coverage:
  `internal/e2e/self_host_block_expr_ir_test.go` (x86-64 + wasm IR).

## Semantics

- **Scope.** The statements run in a fresh child scope. Locals they bind
  are visible to the trailing expression (`Tail`) but do **not** leak
  out of the block:

  ```
  var a: i32 = if (true) { var k = 10; k } else { 0 };
  var b: i32 = if (true) { var k = 20; k } else { 0 };  // separate `k`
  ```

- **Type.** The block's type is the type of `Tail`, checked in the child
  scope. For `if`/`match` expressions, both branches' tail types unify
  to the construct's result type, exactly as single-expression branches
  already do.

- **Value-less blocks.** A block with no trailing expression (its last
  element is a `;`-terminated statement) has type `void`. Using such a
  block where a value is required is a checker error (**E061**) — drop
  the trailing `;` to make the final expression the block's value.

  ```
  // ERROR (E061): the block produces no value.
  var x: i32 = if (b) { var k = 1; } else { 0 };
  ```

  The one context that *wants* a value-less block is a block-shaped
  `defer { … }` / `errdefer { … }` action (`#5153`): the deferred action
  runs for its side effects and its value is discarded, so the checker
  checks its statements directly rather than requiring a trailing value —
  no E061. The action is stored as a value-less `BlockExpr`, so it lowers
  through the ordinary defer machinery on every backend (a value-less
  block leaves nothing on the stack, so no drop follows it).

  ```
  defer { acc = acc + 1; }   // OK: side-effecting, value-less action
  ```

## Implementation map

| Layer | Where |
| --- | --- |
| AST node | `ast.BlockExpr{P, Stmts, Tail}` (`internal/ast/ast.go`) |
| Walkers | `internal/ast/walk.go` (`Walk` / `rewriteExprChildren`) |
| Parser | `parser.parseBranchBody` + `branchStmtStart`, wired into `parseIfExpr` and the `match`-expr arm body (`internal/parser/parser.go`) |
| Checker | `checker.checkBlockExpr` (child scope → statements → tail type); no-tail → **E061** unless the statements diverge (`stmtsDiverge`) → `never`; `assignable` / `unifyIfArms` / match-arm unifiers fold `never`; numeric settle / `postSettleType` recurse into `Tail` (`internal/checker/checker.go`) |
| Interp | `*ast.BlockExpr` arm in `evalExpr` — child env, exec statements, eval tail; a non-normal `r.flow` unwinds as a `controlFlowSignal` that `execStmt` catches (`internal/interp/interp.go`) |
| Compiled lowering | `*ast.BlockExpr` arm in `(*builder).expr` — lower `Stmts` via `b.stmt`, then `Tail` via `b.expr` as the result; a nil (diverging) `Tail` lowers the statements only, leaving the enclosing store unreachable (`internal/ir/ir.go`) |
| Self-host parse | `parse_branch_body` + `branch_stmt_start`, wired into `parse_if_chain` and the match-expr arm body (`examples/self_host/parser.fern`) |
| Self-host lower | `lower_value_tail` — leading statements then the value-producing terminal (`examples/self_host/irlower.fern`) |
| Other passes | `monomorph`, `closureconv`, `boxcapture`, `modload`, `shadowrename`, `treeshake`, `printer`, `format` each recurse into `Stmts` + `Tail` |

## Landed (`#4405`)

- Compiled-backend lowering (wasm / arm64 / x86-64) via the IR.
- Self-hosted-compiler support.
- Workaround deletion in the self-host tree: the "declare a temp, then a
  statement-`match`/`if` to assign it" contortion is now written directly
  as an `if`/`match` *expression* (e.g. `parse_map_lit`'s `map_new` /
  `map_new_i32` ctor selection in `parser.fern`).

## Control-flow inside a value-position block (`#4522`)

A `return` / `break` / `continue` inside a value-position block is
supported on every native backend (interp / wasm / arm64 / x86-64):

```
var x: i32 = { if (early) { return 0; } var k = compute(); k };
```

The interpreter propagates a non-normal `r.flow` out of the
`*ast.BlockExpr` arm as a `controlFlowSignal`, which `execStmt` catches
and turns back into the ordinary `result{flow}` the enclosing loop /
`callFunc` already understand; the block's tail is skipped on the
early-exit path. On the compiled backends the diverging statement
lowers to a branch to the function/loop exit, so the tail value is
produced only on the fall-through path.

### The `never` (bottom) type

A **no-tail** block whose statements *always* exit early (every path
`return`s / `break`s / `continue`s) never reaches a trailing value, so
it is not `void` — it is **`never`** (`ast.NeverType`), the bottom
type, which is assignable to / unifies with any type:

```
var x: i32 = { if (n < 0) { return 1; } return 2; };      // general block
var y: i32 = if (n < 0) { return 1; } else { return 2; }; // both arms diverge
var z: i32 = match (n) { 0 => { return 100; }, _ => n };  // divergent arm
```

`checker.checkBlockExpr` returns `NeverType` (instead of E061) when the
block's statements diverge (`stmtsDiverge`); `assignable` treats
`never` as assignable to everything, and `unifyIfArms` / the match-arm
unifiers fold a `never` arm into the value-producing arm(s). Codegen
lowers the statements only — the diverging terminal makes the enclosing
store unreachable and the ssa lift skips it, so no tail value is
emitted. A genuinely value-less block (last statement is a non-diverging
`;`-terminated statement) still errors **E061**.

Coverage: `TestBlockExprCheckerNeverDiverges` (checker),
`TestBlockExprInterp` `cf-*` cases + `TestBlockExprCompiledControlFlow`
/ `TestBlockExprCompiledControlFlowNoTail` (interp + all native
backends, differential).

## Later slices

- General value-position `{ … }` blocks (needs struct-literal
  disambiguation) — landed for the common cases (`#4521`).
- **Self-host mirror of the control-flow / `never` forms.** The
  self-host desugars value-position blocks (and if/match value-branches)
  to IIFEs (`e_call(e_lambda([], rt, body), [])`); a `return` inside a
  lambda returns from the *lambda*, not the enclosing function, so a
  control-flow-containing block needs an *inline* statement-sequence
  representation there rather than the IIFE. Tracked as native-
  convergence debt (`#4451`).
