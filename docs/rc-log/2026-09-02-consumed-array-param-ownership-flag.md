# A reassigned array parameter owns what it produces (#6644 distcheck, slice 2)

The self-built compiler recompiling its own source (`make distcheck`) was
OOM-killed at 13.9 GB once the correctness half closed
(`2026-09-02-param-strarr-elem-counted-share.md`). That entry's `FERN_RC_TRACE`
pairing put half the live words under the plain `__fern_arr_push`'s pre-grow
buffers. This one names the sites, ports the rule native has for them, and
records why the port moves the reduced shape but not yet the compiler.

## Which pushes grow into a leak

A gdb breakpoint on the allocation inside `__fern_arr_push` in the gcc-linked
stage1, backtraced four frames and aggregated by the first non-runtime frame
(91,963 grows on `lexer.fern`):

| grows | form | site |
|---|---|---|
| 13,166 | plain | `irlower.assign_target_into` |
| 8,735 | owned | `irlower.LowerState.emit` |
| 6,923 | owned | `parser.map_expr_kids` |
| 6,400 | owned | `checker.Scope.bind` |
| 2,894 | plain | `astwalk.ident_of` |
| 1,247 | plain | `astwalk.append_bound_name` |
| 1,172 | plain | `treeshake.ts_name_of` |
| 1,079 | plain | `irlower.decl_is_leaksafe_at_d` |

`owned` is `__fern_arr_push_owned`, which frees the superseded buffer itself.
Every `plain` site at the top is the same shape: an accumulator threaded
through a PARAMETER —

```
function assign_target_into(st: ast.Stmt, acc: string[]): string[] {
    match (st) { ast.StmtAssign(a) => { return acc.append(a.target); }, … }
}
function assign_targets_into(out: string[], stmts: ast.Stmt[]): string[] {
    for stmt in stmts { out = astwalk.fold_stmt_nodes(stmt, out, assign_target_into, …); }
    return out;
}
```

The leaf's `return acc.append(x)` is in-place-exempt (`append_inplace_names_of`),
so it appends into the caller's buffer while capacity lasts and hands back a
fresh doubled buffer when it does not. Every frame above it rebinds a
PARAMETER — `acc = visit_stmt(st, acc)` in `fold_stmt_nodes`, `out = fold(…)`
in `assign_targets_into` — and the self-host's rule for a parameter rebind was
absolute: *the caller owns it, release nothing*. So at every level, every
generation the chain grew was orphaned. A 40-element accumulation leaks the
4-, 8-, 16- and 32-slot buffers; the compiler runs that walk at the head of
three release analyses, once per nested block.

Reduced, 50 rounds of a 40-name fold, x86-64:

| compiler | allocs | frees |
|---|---|---|
| native | 295 | 295 |
| self-host, before | 386 | 86 |
| self-host, after | 386 | 336 |

The 50 that remain are the `[]` seed temp handed to the first frame, one per
round: the argument-temp sibling native closed in
`2026-09-02-consumed-array-arg-temp.md`, its own slice here.

## The rule native has and the self-host did not

Native promotes a reassigned, non-`own` array parameter to *consumed-threaded*
(`computeConsumedParams`) and gives it a hidden ownership bit instead of an
entry retain (`isConsumedArrayParam`), because rc == 1 is what the in-place
push gates on: a retained parameter would enter at rc 2 and copy the whole
buffer on every append it ever sees. The overwrite rule
(`emitConsumedArrayOverwriteDec`) is

```
if (new != old) { if (owned) dec(old); owned = 1; }
```

A different pointer means the right-hand side handed the slot an owned
reference and the old one lost this binding — the caller's borrow while the
bit is 0, a superseded buffer of the frame's own once it is 1.

The self-host now carries the same bit, `$ownflag_<param>`, allocated and
zeroed in `lower_func` for every array parameter `body_assign_targets` names
that is not declared `own`. It differs from native in one respect that follows
from the runtimes: the self-host's in-place push returns the same pointer with
the count untouched (native's bumps it to 2 and decs it back), so the
same-pointer arm here is empty. Three sites read the bit:

- **every store into the parameter** — `emit_arr_store` routes a flagged slot
  to `emit_consumed_param_store`, and the self-append and `.with` arms that
  stored directly now go through it. The release is the buffer-only
  `__fern_rc_dec`: a grown copy shares the old buffer's elements without
  retains, so an element walk would free what the new buffer holds.
- **`return <param>`** — the return-transfer retain (#4357) exists because the
  buffer is the caller's; when the bit is 1 the frame owns it and hands it over
  as it stands. Without this the frame's last generation left at rc 2 and the
  caller's sweep only brought it to 1.
- **every return** — a flagged parameter the returned value does not name is
  released if the bit is 1 (`emit_consumed_param_exit`), also at a bare
  `return;` and the `?` failure edge. Naming it anywhere in the value is the
  conservative skip: the value may be that buffer or alias it.

The container-read alias retain on reassign (`p = h.names`, `p = g[i]`), which
was gated to locals because a parameter's old value was never released, now
admits a flagged parameter too — the store below it releases the old value, so
the new one has to be counted.

## Measured on the compiler: the rule lands beside the leak, not on it

`checker.fern`, leak-check-instrumented builds, `-emit asm`:

| compiler | allocs | frees | live_bytes |
|---|---|---|---|
| stage0 — native-built | 19,645,369 | 12,987,673 | 441 MB |
| stage1 — self-built, before | 19,509,722 | 4,929,815 | 2,958 MB |
| stage1 — self-built, after | 19,551,926 | 4,958,676 | 2,995 MB |

The frees barely move, and `FERN_RC_TRACE` on `lexer.fern` still shows 66,447
blocks under `__fern_arr_push`. Listing which functions received a flag
explains it: 214 parameters across the compiler, `astwalk.ident_of`,
`assign_targets_into`, the assembler's `buf` threading — but **not
`astwalk.fold_stmt_nodes`**, the frame every accumulator walk threads through.
It is `fold_stmt_nodes[T](st, acc: T, …)`, an UNBOUNDED generic, and the
self-host handles those by erasure (`parser.fern`'s `type_params` note): one
body serves `T = string[]`, `T = ast.Expr[]`, `T = boolean` and `T = string`,
so its `acc` slot has no array type to be flagged on, and the generation the
leaf hands back to `acc = visit_stmt(st, acc)` is orphaned there. The flagged
`assign_targets_into` one frame up only ever sees the final buffer.

So the rule is right and is pinned, and the compiler's own dominant leak sits
one erased frame above where it applies. The next slice is the erased
accumulator: either the monomorphiser clones an unbounded generic whose type
parameter reaches an array position, or the release is made layout-agnostic so
an erased slot can carry the bit. Both are the compiler's to decide, not a
rewrite of `astwalk` around it.

## The other finding: native assembly in a self-built compiler

Running stage1 with `-o <binary>` (its own x86 assembler, `x86_native.fern`)
on `lexer.fern` exhausts the arena (exit 125) in 13 s — with the pre-change
compiler as much as with this one. `FERN_RC_TRACE` on that run:

| leaked blocks | site |
|---|---|
| 117,270 | `__fern_arr_push` — copied `code` buffers, 11.4 G words in total |
| 239,027 | `__fern_str_concat` |
| 39,308 | `x86_native.x86_str_split` |

The assembler threads `a.code` through helpers as
`a = X86Asm { ...a, code: x86_le32(a.code, 0) }` on an `own a`. The helper's
`return buf` carries the return-transfer retain, so the buffer comes back at
rc 2 whenever the append stayed in place; nothing on the struct-update side
balances it, the next helper's push finds a shared receiver and copies the
whole ~445 KB buffer, and the superseded one is never released. One copy and
one leaked buffer per emitted instruction is exactly what 8 GB in 13 s looks
like. This is what `make distcheck`'s stage2 runs into first, and it is the
struct-update accounting, not the parameter rule — its own slice.

## Pinned

`conformance/cases/array_param_threaded_reassign`: the fold chain, a
self-appending parameter (`string[]` and `i32[]`), early returns before and
after the first rebind, a rebound parameter that is not returned, and a
parameter rebound to an alias of another array — every result read back by
bytes after 400 churn allocations, so an early release is a wrong exit code and
not only a count. Elements are literals and every seed is a local, so the leak
census sees this shape alone: 440 unpaired allocations before, 0 after, on the
same exit code as native and the interpreter.
