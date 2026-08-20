# A parameter is a declaration too

`join_strarr_init` and `tostr_scalar_init` both settle a credit by reading the
RECEIVER's declared type — the mechanism
`2026-08-20-join-result-fresh-credit.md` chose over a slot flag. Both read the
same name/type pair, and that pair was harvested only from the body's annotated
`var`s. A parameter is a declaration that never appears as a `var`, so every
param receiver was refused.

400 rounds of the churn harness, a pair of compilers from the same commit:

| shape | x86-64 | arm64 | wasm |
| --- | --- | --- | --- |
| `f(xs: string[])` → `var s = xs.join(sep)` | 131200 → **0** | 131200 → **0** | 128000 → **0** |
| `f(n: i32)` → `var s = n.to_string()` | 12800 → **0** | 12800 → **0** | 9600 → **0** |

One change, two classes, because they already shared the harvester.

## Fix

`param_decl_names` / `param_decl_types` extend the pair with the function's
`ParamDecl[]`, which `reclaimable_names_of` now takes. Body declarations are
seeded FIRST and parameters after: the lookups take the first match, so a local
shadowing a parameter still answers with its own type.

That is an eleventh parameter on a function that already had ten, which is worth
a second look before accepting. It passes: `fn.params` is the function's own
declaration data, the same category as the `body` and `structs` already there,
and it is the missing INPUT rather than a value threaded to one caller to dodge a
fix. The alternative — a shared record for the fn's declaration data — is a
refactor of ten existing arguments, not of this one.

## The gate still holds, and it is checked

Seeding parameters widens what the type tests SEE, not what they accept. Probed
with both classes at once, a struct param carrying user methods that return an
alias of a field the receiver still owns:

```fern
function (h: Holder) join(sep: string): string { return h.name; }
function (h: Holder) to_string(): string { return h.tag; }
```

Clean on all three backends, before and after — `join_strarr_init` requires the
declared type to be `string[]` and `tostr_scalar_init` requires an int scalar, so
a `Holder` receiver is refused whether it is a local or a param. Both shapes are
now standing negative cases in their suites.

## What is left

An UNANNOTATED local receiver (`var xs = base.split("-")` with no `: string[]`)
still has nothing to read and is still refused. That is the honest remaining
limit of declaration-reading, and it is sound in the direction that matters: the
wrong answer on this side is a leak, not an over-release.
