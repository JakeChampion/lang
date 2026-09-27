# 2026-09-27 — an `own` array alias is threaded like the parameter (#10357)

```fern
function at_node(n: i32, own a: string[]): string[] { return a.append("g" + ""); }
function fold(own acc: string[], n: i32): string[] {
    var a: string[] = acc;
    a = at_node(n, a);
    return a;
}
// main: pending = fold(pending, fd), twelve times
```

On the AST lowering this was a use-after-free under `FERN_SANITIZE=1`.
`var a = acc` is a move-on-alias, so `a` counted as a local the frame owns,
and `a = at_node(n, a)` released the buffer `at_node` had outgrown. `main`'s
rebind released the same buffer again.

## The rule

The AST lowering has no element-unit protocol for a pointer-element `own`
array: a grown copy shares the old buffer's elements without retains. So the
caller keeps the release of the buffer it passed. A result that differs from
it is released by the caller's rebind, and an identical one comes back
uncounted. Native and the semantic lowering have the callee consume instead,
and so does this lowering for a scalar-element `own` array and for a position
`own_consumed_positions` names. Moving pointer-element arrays onto that rule
needs the element units first, and the semantic lowering already has them.

So the fix makes the frame agree with the caller-releases rule everywhere:

- A reassigned `own` array parameter that the frame does not consume
  (`threaded_own_array_param`) gets the ownership flag a reassigned borrowed
  array parameter already has. Flag 0 means the slot holds the caller's
  buffer, and flag 1 means it holds a replacement the frame minted. A rebind
  releases only a flag-1 value, and so does the exit.
- A top-level `var a = p` where p is not named again
  (`own_handbacks_of`, `bind_own_handback`) binds `a` as a borrow that
  continues p's flag. A return of `a` with the flag clear hands the buffer
  back uncounted, as `return p` does. `var a = g(p)` with p at an `own`
  position that g does not consume is the same bind: while `a` holds p's
  buffer the flag is p's, and once it holds another the flag is set and a
  buffer p owned is released. An alias of either does the same. The last
  one is the spelling #10338's `inst_stmt` uses
  (`var out = astwalk.fold_stmt_own_pruned(st, acc, …)`). Its stage-2
  compiler freed `pending` there.
- Such an alias is not a grow alias. The caller handed the buffer over to be
  grown, as the direct `acc = at_node(n, acc)` spelling already assumes. Left
  in `grow_alias`, every call bracketed `a` and copied the whole buffer.
- `own_consumed_positions_of` follows the alias. Otherwise an AST `fold` that
  passes `a` to a produced consuming callee kept a threaded position that the
  callee then consumed. The lowering and the registry find these binds with
  one function, `own_handbacks_of`, over the same "callee|idx" rows. A
  function value's rows are read the way `stamp_fnval_decl` stamps them
  (`fn_value_own_rows`).
- A flagged parameter whose position consumes starts its flag set: the frame
  owns that unit, so a bind that supersedes it releases it, and so does a
  return that drops it (#10361).

The first rule also fixes a leak on the direct spelling: with several grows in
one frame, `acc = at_node(…, acc)` repeated leaked every intermediate buffer.

## Measured

`TestSelfHostOwnParamAlias*`: 19 shapes × {semantic, AST, AST main, AST fold
with produced callees} × {x86-64 leakcheck + sanitizer, arm64, wasm}. On main,
24 of the first 14 shapes' 56 x86-64 legs failed. They are all green now, and #9409's
reproducer (`borrowed_outer`) is among them.

The AST-lowered driver built from #10338's sources compiled
`sort_wider_test.fern` into a use-after-free under `FERN_SANITIZE=1`. With
this change it finishes, and `TestSelfHostStage2FixpointArm64` passes on that
branch.

## Not covered

- #10360: a struct-array local reassigned from a call leaks its elements on
  the AST lowering, with or without `own`. `struct_elem_walk` pins that leak
  (89 allocs / 73 frees), and `struct_elem_scope` is held to the sanitizer
  only.
- #10367: a local aliasing a borrowed array parameter leaks when a branch
  appends to it. The checker's `inst_stmts` has this shape, so its AST build
  still leaks there.
