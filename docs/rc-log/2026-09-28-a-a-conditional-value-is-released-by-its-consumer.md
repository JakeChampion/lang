# 2026-09-28 — a conditional value is released by its consumer; block locals earn credits (#10438)

AST-lowering (`FERN_SEM_IR=`) leaks and miscompiles. The semantic lowering
balanced every program below already.

## An if, match or block value at a consumer that does not bind it

```fern
t = t + sum(if (j > 1) { [j] } else { [1, j] });
t = t + sum({ var q = [j, 1]; q });
```

The value reaches the call through the `$ife` / `$vbi` temp, which nothing
sweeps, so only a binding ever released it. It is now released by a call
argument, a `.len()` receiver, an index read and a field read when it is
owned whichever arm ran. That is native's `countedConditionalType` rule: every
arm yields a reference the arm retained, or a value that is already owned.

The arms retain an array local and nothing else: `lower_value_tail`'s
`ifexpr-alias-leaf`, the match arm's `tmp = E` store (`reassign_inc`), and a
block's tail local, which the block moves out or retains. So:

- an array value is owned when every leaf is an array local or a fresh
  scalar-element array (a literal, a slice, an `ARR:` / leak-safe `ARROWN:`
  producer, or such a value nested). It is judged after the value is lowered,
  so a block's tail local resolves by its binding site. A match temp typed as
  an array releases its old value at each store, so it disqualifies the value.
- a string value is owned when every leaf is a fresh string, and a struct
  value when every leaf is a base-less literal or a strict-fresh producer's
  result of one type. A block's tail local counts when its initializer does
  and nothing after it lets the local escape.

The releases reuse each consumer's existing path: `$laa` / `stash_fresh_str_arg`
/ `stash_fresh_struct_arg` at a call, the `$lenra` / `$lenr` receivers, the
`$ocidx` index spill and the `$ocfld` field read. A struct passed at a `dyn`
parameter is the struct box, so that position now takes the struct stash too.
A fresh string index base (`(s + "w")[2]`) is now spilled and freed as well.

## The checker typed no `{ …; tail }` block

`check_call_expr` typed an if or match IIFE by its arms and returned unknown for
a block, so annotate stamped no type on the call. The lowering then guessed:

- `({ var q = s + "z"; q }).len()` read the string as an array: x86-64 answered
  13 for 92.
- `measure({ var q = s; q })` boxed a struct as an `i32` dyn cell, and dispatch
  hit `unreachable` (the call-argument program of #10529's native fix: exit 134
  for 102).
- `({ var q = P { … }; q }).y` refused to lower.

The block is now typed by its final `return` with the leading statements bound
(an earlier `return` leaves the function). A bare literal tail stays unknown, as
`inferred_lambda_result` keeps it. `type_to_irtag` spells a `dyn` type, so a
block yielding a `dyn` local is typed too. The self-host checker now reports a
block's type mismatch as E003, as native does, instead of "could not infer".

## A value block's own locals earned no credit

The credit collectors walk statements and never enter an expression, so a
local a value block declared was invisible to them. `lower_func` now hands
`reclaimable_names_of` a view of the body in which each value block's
statements (less the value it yields) also stand as a nested block ahead of the
statement that holds it. The statement still carries the block, so the escape
walkers read the yielded value there, as a value. The reassignment set is read
from the same view.

## A bound struct-array element

`var p0 = q[0]` over a struct array refused the array's element credit. It is
now a counted share: the bind retains the element box, the binding takes a
box-only struct credit (`ELSHARE:` + `NODEEP:`), and `structarr_elem_escapes`
forgives exactly those sites. Admitted for an all-scalar element struct, over a
`q` the function declares once, into a `p` never reassigned and never escaping.

## An element handed back by a callee (found on the way)

Admitting the element bind exposed a use-after-free on main. The STRUCTARR gate
read a bare array argument through the box flag alone, which says the callee
keeps no reference to the buffer and nothing about an element it hands back:

```fern
function keep(q: P[], i: i32): P { return q[i]; }
…  a = keep(q, 1);   // q's element walk frees the box a holds
```

x86-64 answered 164 for 234 once the freed box was reused. The gate now asks
the element question the arrstruct and arrenum twins already ask (`ELB:` or
`CNT:`, or a callee whose every result is counted), for a struct return, a
generic one and a `dyn` one alike.

## Measured (x86-64 `FERN_LEAKCHECK=1`, AST lowering; wasm matches)

| program | before | after |
|---|---|---|
| #10438 argument repro | 6 / 3 | 6 / 6 |
| #10438 block argument repro | 3 / 0 | 3 / 3 |
| #10438 block struct-array local | 20 / 10 | 20 / 20 |
| #10438 element bound, block yields | 20 / 10 | 20 / 20 |
| element bound in a loop body | 20 / 10 | 20 / 20 |
| `condArrReleaseSrc` | 33 / 4 | 33 / 33 |
| `condStrReleaseSrc` | 31 / 1, exit 224 (interp 19) | 31 / 31, exit 19 |
| `condStructReleaseSrc` | refused | 28 / 28 |
| `condStructArrLocalsSrc` | 70 / 30 | 70 / 70 |
| `condElemHandoutSrc` | 60 / 60, exit 55 (interp 75), sanitizer use-after-free | 60 / 60, exit 75 |
| `condNotOwnedSrc` | 18 / 0 | 18 / 0 |

`TestSelfHostConditionalValueRelease{X86_64,Arm64,Wasm}` holds the six programs
to the interpreter's answer on both lowerings: a balanced census for five,
and no more frees than allocations for `condNotOwnedSrc`, with the sanitizer
silent on x86-64.

## Not covered

- An arm yielding a string, struct or `dyn` local is not retained, so a
  conditional mixing one with a fresh arm leaks the fresh arm at every
  consumer and binding. Retaining it needs the bindings credited in step
  (#10570).
- A parameter yielded by a conditional reads as escaping to the borrow
  inference (`sum(if (c) { a } else { b })` inside a callee), so its callers
  keep their argument (#10571).
- A `dyn` local declared in a loop body frees only its last value, and a fresh
  value from a `dyn`-returning producer passed at a `dyn` parameter is never
  released (#10572).
