# 2026-09-21 — a generic enum keeps its arguments

`examples/tests/sim_driver_test` produced **0 of its 180 declarations**:

```
FERN_SEM_IR: sim____pend__i32$clo0: unresolved result type: declared `async__Future`
FERN_SEM_IR: sim____pend__i32: call target has no semantic contract: sim____pend__i32$clo0
```

Seventeen lines with no standard library reproduce it:

```fern
enum Box[T] {
    Now(T),
    Later(i32, (i32) => Box[T])
}

function hold[T](tok: i32, next: Box[T]): Box[T] {
    return Later(tok, (w: i32) => next);
}
```

## One arm of the type resolver was name-only

`type_from_ref_names` resolves an instantiation by its base name. Three arms
handle a bracketed spelling, and two of them carry the arguments:

- a RESERVED enum (`Option[i32]`, `Result[T, E]`) → `t_union_g(r.base, uargs)`,
  with the comment that the typed-IR annotation needs the full tag;
- a user generic STRUCT (`Box[i32]`) → `t_struct_g(r.base, gargs)`, with the
  comment that field access needs them to substitute a type-parameter field;
- a user generic ENUM (`Box[i32]`) → **`t_union(r.base)`**, dropping them.

So the checker typed `next` as the bare `Box`. Nothing complains at that point:
assignability ignores union arguments, and the program checks and runs. It goes
wrong when the spelling is read BACK. `inferred_lambda_ret` stamps a lambda
that spells no result with `typeinfo.spelling` of what its body returns, and
that stamp becomes the declared return of the hoisted `<fn>$clo0`. It said
`Box` — the GENERIC, which the enum monomorphiser drops once it has cloned
`Box__i32`. semsource then had a declaration naming a type that no longer
existed, refused it for an unresolved result, and every caller followed through
`call target has no semantic contract: <fn>$clo0`.

The fix is the struct arm's own three lines, one loop down.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`.

| program | before | after |
|---|---|---|
| `examples/tests/sim_driver_test` | 0 of 180 | **180 of 180** |

It passes 8 of 8.

Corpus census, 864 seeds, both columns against frozen binaries:

| | before | after |
|---|---|---|
| programs produced whole | 843 | **844** |
| declarations produced | 77,752 of 78,504 | **77,932 of 78,504** |

**Exactly one file moves and +180 is exactly its gap.** Nothing regresses. The
remaining corpus gap is 572 declarations over 20 files.

On the test-row program the typed leg reclaims the shape whole,
`allocs=3 frees=3 live_bytes=0`, where the AST leg frees **nothing** and
strands 128 bytes.

## What it does not reach

A CONCRETE instantiation of the same enum — `hold(tok: i32, next: Box[i32])`
rather than `hold[T]` — still refuses, now further along: the hoisted closure's
declared return is the correct `Box__i32`, but the captured value's type inside
the closure body is still the bare `Box`, so it refuses `return type: declared
Box__i32, returns Box`, and the creator refuses `closure capture type`. That is
the capture's spelling rather than the annotation's, a separate site with a
separate cause, and it is not what any corpus program hits.

## Tests

`a-generic-enum-keeps-its-arguments` — 3 of 3, `noLeak`, all four targets,
differential against the AST leg. The enum is recursive through a callable
payload, which is the shape that forces the spelling to be read back at all.

That the row can FAIL was checked rather than assumed: with `checker.fern`
reverted it goes red on all four targets, on the produced count and on
`noLeak`.
