# The accumulator behind an erased generic gets a type (#6644 distcheck, slice 3)

`2026-09-02-consumed-array-param-ownership-flag.md` ported native's ownership
flag for a reassigned array parameter and then measured almost nothing on the
compiler: 66,447 pre-grow buffers still under `__fern_arr_push` on
`lexer.fern`. The flag never reached the frame that orphans them.

## Where the rebind happens

Every accumulator walk in the compiler threads through `astwalk`:

```
pub function fold_stmt_nodes[T](st: ast.Stmt, acc: T, visit_stmt: (ast.Stmt, T) => T, …): T {
    acc = visit_stmt(st, acc);
    …
}
```

`T` is an UNBOUNDED type parameter, and the self-host compiles those by
erasure: one body serves `T = string[]`, `T = ast.Expr[]`, `T = boolean`,
`T = string`, `T = checker.Annot[]`. In that body `acc` has no type. The
lowering cannot tell whether the value `acc = visit_stmt(st, acc)` replaces is
an array it now owns or a scalar it must not touch, so it does nothing — and
the fresh buffer the leaf's `return acc.append(x)` hands back after a grow
replaces the old one with no release. Type-directed ownership has no purchase
on a slot without a type; the fix is to give it one.

## Promotion clause (d)

The parser already promotes an unbounded parameter into the monomorphised set
for four signature-shaped reasons (clauses a–c″ in `parse_func_decl`). This
adds the first clause that reads the BODY: a bare-var parameter the body
reassigns, or a `var x: T` local, is a value the frame binds without knowing
its type. Such a function is cloned per instantiation, the way a bounded
generic is, so the clone's accumulator is a `string[]` the ownership flag
applies to. Two constraints carried over from the existing clauses:

- every still-erased variable of the function is promoted together, and only
  when each is bindable from a parameter (the clause-(b) test), so the clone is
  fully concrete and no sibling survives erased;
- free functions only, for the no-stranded-receiver-var reason clause (c-arr)
  gives.

Two things the first attempt taught:

**Mutual recursion.** `map_stmt_acc[T]` binds `var a: T` and calls
`map_stmts_acc[T]`, which reassigns `acc` and calls back. Promoting one and not
the other leaves an erased body calling a template the monomorphiser has
dropped — an undefined reference. The `var` binding is the same blindness as
the reassign, and including it promotes both.

**Forwarders.** `fold_stmt_own[T](st, acc: T, v)` neither binds nor reassigns;
it hands `acc` to the promoted `fold_stmt_nodes`. Its call site then binds the
callee at the type VARIABLE, a clone keyed `T`, erased inside — the spurious
clone the monomorphiser's own notes warn about. `promote_erased_forwarders`
runs before instantiation and promotes, to a fixpoint, any erased generic that
passes an erased-var-typed parameter to a bounded generic. After it no clone
in the compiler is keyed on a variable.

**Tuple destructures.** Inside the clones, `var (ns, na, nh) = visit_stmt(st,
acc)` left `na` untyped in the monomorphiser's environment, so the recursive
`map_stmts_acc(body, na, …)` could not bind its instantiation either. The
StmtVar rewrite now types each comma-joined binder from the matching element
of the init's tuple type (`menv_bind_destructure`).

## What it monomorphises

In the compiler itself: the eight `astwalk` fold and map walkers and
`util.append_all`, 86 clones in all — `fold_stmt_nodes` at `string[]`,
`ast.Expr[]`, `boolean`, `i32`, `string`, `checker.Annot[]`,
`asmcore.UnkScan`, `parser.CFScan`; `map_*_acc` at `embed.FoldAcc` and
`irlower.InlineCloAcc`. 6,050 functions against 5,997.

## Measured

Reduced, a generic `fold[T](xs, acc: T, f)` instantiated at `string[]`,
`i32[]` and `i32`, 20 rounds of 45 names:

| compiler | allocs | frees |
|---|---|---|
| native | 365 | 365 |
| self-host, before | 486 | 266 |
| self-host, after | 486 | 426 |

The 60 left are the function-value boxes for the three callbacks per round —
the fn-value argument class, not this one.

`checker.fern`, leak-check-instrumented builds, `-emit asm`:

| compiler | allocs | frees | live_bytes |
|---|---|---|---|
| stage0 — native-built | 19,645,369 | 12,987,673 | 441 MB |
| stage1 — self-built, flag only | 19,551,926 | 4,958,676 | 2,995 MB |
| stage1 — self-built, promoted | 20,102,293 | 5,094,478 | 3,078 MB |

`lexer.fern`: 940,159 allocations, 233,442 frees, 72.2 MB live — unchanged.

The clone has the type and the flag, and the count still does not move: its
every `return acc` now carries the bare-parameter return-transfer retain,
which the frame above sees come back at the same pointer and leaves alone. The
next entry, `2026-09-02-identity-return-counted.md`, is that.

## Pinned

`conformance/cases/generic_fold_accumulator_reclaim`: the generic fold at three
instantiations, every result read back after churn. Leak-checked, 486
allocations against 266 frees before and 426 after; the 60 left are the
function-value boxes for the three callbacks per round, which the census does
not count, so its row is pinned at 0 against 160 before.
