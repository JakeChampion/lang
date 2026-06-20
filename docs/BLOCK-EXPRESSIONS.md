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

## Slice 1 scope (this document)

Slice 1 is deliberately narrow:

- **Only `if` / `match` *expression* branches** parse a block-with-tail.
  Concretely: the `{ … }` after `if (cond)`, after `else`, and after a
  `match`-arm `=>` may now be a `{ stmts; tail }` block.
- A branch that is a **single expression with no leading statements**
  stays a bare expression (`{ 1 }` is the value `1`, not a `BlockExpr`),
  so existing single-expression `if`/`match` expressions are unchanged.
- **Statement-position `if`/`match`** (function bodies, loop bodies,
  general statement blocks) are **untouched**. Only the if/match
  *expression*-branch parse path changed.
- **General value-position `{ … }`** elsewhere is NOT a block-expression
  yet (it collides with struct-literal syntax — a later slice).
- **Interpreter only.** The native interpreter (`fern -interp`)
  evaluates block-expressions; the compiled backends (wasm, arm64,
  x86-64) **reject `BlockExpr` cleanly** with an unsupported-feature
  error (no panic). This mirrors how `dyn` dispatch and the `as?`
  downcast shipped interp-first.
- The **self-hosted compiler** does not handle block-expressions yet —
  also a later slice.

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

## Implementation map

| Layer | Where |
| --- | --- |
| AST node | `ast.BlockExpr{P, Stmts, Tail}` (`internal/ast/ast.go`) |
| Walkers | `internal/ast/walk.go` (`Walk` / `rewriteExprChildren`) |
| Parser | `parser.parseBranchBody` + `branchStmtStart`, wired into `parseIfExpr` and the `match`-expr arm body (`internal/parser/parser.go`) |
| Checker | `checker.checkBlockExpr` (child scope → statements → tail type); error **E061**; numeric settle / `postSettleType` recurse into `Tail` (`internal/checker/checker.go`) |
| Interp | `*ast.BlockExpr` arm in `evalExpr` — child env, exec statements, eval tail (`internal/interp/interp.go`) |
| Compiled reject | `ir.rejectBlockExpr` (clean `WalkProgram` scan) + a defensive lowering-switch guard arm (`internal/ir/ir.go`) |
| Other passes | `monomorph`, `closureconv`, `boxcapture`, `modload`, `shadowrename`, `treeshake`, `printer`, `format` each recurse into `Stmts` + `Tail` |

## Later slices

- General value-position `{ … }` blocks (needs struct-literal
  disambiguation).
- Compiled-backend lowering (wasm / arm64 / x86-64).
- Self-hosted-compiler support.
- Control-flow (`return` / `break` / `continue`) inside a value-position
  block-expression — currently rejected by the interpreter, since slice
  1 doesn't thread control flow through expression evaluation.
