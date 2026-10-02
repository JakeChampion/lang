# An enum call result carries a count

2026-09-27 — `fresh_enum_fwd_fixpoint`, `collect_counted_enum_local_sites`,
`ssarc.caller_sigs`. Fixes #10365; refs #10310, #10410, #4451.

On the AST lowering a caller released an enum call result only when the callee
returned a variant constructor directly, and then only for an rc payload bound
by `RCE:`. A scalar-payload ctor producer's result, and every callee returning
an enum local, handed the caller a box nothing released.

## The contract

`ENUM:` (the enum half of `return_fresh_struct_ret_fns`) now names the free
functions whose every return hands the caller one count:

- a fresh variant ctor, or a forwarding call to a member, as before;
- a never-reassigned local built by either, escaping only by the return. Its
  box is this frame's own whether a `SCENUMS:`/`RCENUMS:` credit moves it out on
  return or no credit touches it;
- a never-reassigned `let g = h.e` field alias. Its `EALIAS:` credit dups it and
  the move-on-return keeps it; where no credit took it, the return retains it
  (`ERETALIAS:`, `ret_enum_alias_uncounted`).

A caller binding `let a = f(..)` of a member (not an `RCE:` one, which keeps its
own credit) is tagged `ECALL:` under the same reassign and escape gates as
`EALIAS:`, and takes the same rc-gated release (`credit_counted_enum_local`):
the gated deep drop where the enum's walk exists, else the box-only dec. The
result may be shared — a produced callee's always may — so a sole-owner release
would be wrong.

Two consumers the registry already fed now see the wider set: a struct literal
field takes the count over with no retain, and the scalar arg stash releases it
after a borrowable call. A third joins: an `E[]` array literal of member calls
earns the `ARRENUM:` credit, whose element walk now uses the `is_unique`-gated
drop because an element may be shared.

At the semantic boundary `caller_sigs` wrote `ENUM:` only for a box-only union
result. A produced callee returns one counted reference for any union, so every
union result gets the row, and an AST caller of a produced rc-payload callee
releases it too.

## Measured

x86-64, 100 rounds, allocs/frees, answer interpreter-confirmed:

| program | lowering | before | after |
|---|---|---|---|
| `enumFieldAliasReturnSrc` | AST | 900/500 | 900/900 |
| same | AST main, produced callees | 900/500 | 900/900 |
| `enumCallResultSrc` (ctor, local, chain, alias, branch, struct and array stores) | AST | 3150/1250 | 3150/3150 |
| same | AST main | 3150/1350 | 3150/3150 |
| a scalar-ctor producer bound to a local | AST | 100/0 | 100/100 |

With the rc plan off (`FERN_SELFHOST_RC_PLAN=0`), where `EALIAS:` admits fewer
aliases, a probe mixing returned aliases, fresh locals and forwarding calls
answered 66 where 64 is right; it answers 64 now. The cause was not isolated.

## Still open

A callee returning a borrowed parameter, or a parameter's field, is not a
member: the result is the lender's box, and the lent local escapes into a call
that is not borrowable, so nothing releases it (1100/801 on the AST lowering,
#10410). Retaining on that return without the caller treating the lend as a
counted handback only moves the leak onto every temporary-position call.
`TestSelfHostEnumCallHandbackX86_64` pins the answer.

A call result used as a temporary outside a borrowable position, or as a
`match` scrutinee, still leaks its count.
