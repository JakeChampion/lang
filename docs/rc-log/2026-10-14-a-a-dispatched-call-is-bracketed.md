# 2026-10-14 — a dispatched call is bracketed

`compiler/ssaunits.fern`: `dispatched_rows`, `indirect_callee`,
`grow_callees`. `compiler/ssarc.fern`: `dispatch_bracketed`. Fixes #12373.
Changes emitted code.

## The failure

A body that appends to a borrowed array parameter grows it in place when the
box's count says it is sole. That is sound only because a caller that still
reads the array brackets the call, holding a count across it. `bracketed`
found the rows to bracket by the callee's name. A call through a function
value has no name, and a dyn call's name is not any body's, so neither was
ever bracketed. The body then grew a buffer its caller still read.

Measured on main with the self-host x86-64 build:

| program | interpreter | before |
|---|--:|--:|
| `apply(grow, a)`, `a` read after | 72 | 102 (`a` grew) |
| `apply(take, bag)`, `take` appends to `bag.items` | 23 | SIGSEGV |
| `g.grow(a)` on a `dyn Grower` | 22 | 33 (`a` grew) |
| `h.add(3)` on a `dyn Holder`, appending to `self.items` | 23 | SIGSEGV |

The record cases crash because a field append on a sole record moves the
buffer out and nulls the field (`append_field`), so the caller's record loses
its field.

## The fix

After the grow table's fixpoint settles, `dispatched_rows` adds rows for the
calls that name no body:
- every position a closure body may grow becomes a row of
  `indirect_callee()` (`&`), the name a call through a function value reads;
- every position one of a dyn call's arms may grow becomes a row of that dyn
  call's own name.

A body's parameter p is the call's operand p in both cases, since the
environment and the receiver are both operand 0. Nothing in the fixpoint
reads these rows.

`dispatch_bracketed` holds the whole box of every lent operand at such a
position, unless the operand is the frame's own and dies at the call. A
field is grown only under its record's own count (`append_field`,
`with_field`), and a dyn value is its concrete's box, so raising the box's
count makes every field test answer "shared". A link dying at the call is
still bracketed, since its box may be the caller's.

A parameter handed straight on to a dispatched call is bracketed in the
frame that hands it, not in that frame's callers through a row of its own:
a whole-box row would need a record bracket the direct-call rows do not
carry. A wrapper like `apply(f, xs) = f(xs)` therefore copies when `f`
grows, even for a caller done with `xs`. A value the frame owns and lends
as the last reader is still grown in place: the fold in
`TestSelfHostDispatchedLastReaderInPlace` appends 4,096 elements through a
function value at one allocation per doubling.

## Tests

`TestSelfHostDispatchedBracket{X86_64,Arm64,Wasm}` run the four programs
above and a capturing closure under the leak census on all three targets,
and under the sanitizer on x86-64. Each row checks the old value in a binding the
caller reads after the call.
