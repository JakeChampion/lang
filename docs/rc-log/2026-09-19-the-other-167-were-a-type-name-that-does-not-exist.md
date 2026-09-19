# 2026-09-19 — the other 167 were a type name that does not exist

`2026-09-19-the-sidecar-was-twelve-of-the-two-hundred-and-thirty-eight.md` left
226 `unresolved result type` refusals and named the split: 167 on `__lam_N`
owners, 59 on `$iife` ones, with "whatever leaves those unresolved is not the
sidecar" and the next step being to reduce one.

Reducing one took four programs. All 167 were this:

```fern
function main(): i32 { var v: boolean = if (true) { false } else { true }; … }
```

An if-expression in value position desugars to a 0-arg IIFE whose `ret_type`
`if_expr_rt` reads off the then-branch. For a boolean branch it returned the
string `"bool"`. **The language has no type named `bool`.** The checker rejects
it by name and even offers the fix — `checker.fern` answers
`" (did you mean \`boolean\`?)"` — so `scope.ret_type` came back unresolved, and
`semsource.build` refused the lambda with an empty result spelling.

One word.

## Why it survived

Four things had to line up, and all four did.

**The AST lowering accepts it.** `asmcore` treats `"bool"` as an internal tag
beside `"boolean"` in four places, because `ty_tag` spells the boolean type that
way. So the desugared IIFE compiled and ran correctly on the AST path, which is
the oracle every differential test compares against. Nothing was ever wrong in
the output — only in what the typed path would accept.

**The tag is non-empty.** `infer_ret_types_module` revisits a lambda whose
`ret_type` is empty. `"bool"` is not empty, so the inference that would have
supplied the right spelling never ran. The same trap the wide-integer-literal
fix in this function already documents, from the other direction: there the tag
was present and wrong about width, here it is present and not a type at all.

**The other branch kinds are spelled correctly.** `"string"`, `"i32"`, `"f64"`,
`"i64"` are all real type names. A census reader sees a bucket of 167 and looks
for a missing mechanism, not for one arm of a five-arm match spelling its own
tag wrong.

**The refusal names nothing.** `unresolved result type: ` prints
`typeinfo.spelling(s.result)`, and an unresolved type spells empty. The message
that would have ended this in a minute — `unresolved result type: bool` — is
the one message it cannot print, because a type that failed to resolve has no
spelling to report. Worth fixing separately: the refusal should carry the
DECLARED spelling it failed on, not the resolved type it did not get.

## What it bought

The same 512 fernsmith programs:

| | before | after |
|---|---|---|
| produced whole | 285 | **352** |
| `unresolved result type` | 226 | **59** |
| `no semantic contract` | 208 | **37** |
| `function address is not a closure value` | 30 | 31 |
| verifier: `function value is not an element` | 42 | 48 |
| total refusals | 1035 | **717** |

**+67 modules.** The `__lam_N` half of the bucket is gone entirely — every one
of the 59 left is a `$iife` owner, which is the split the previous entry
predicted and the reason it predicted it: an IIFE is not a declaration and has
no signature to carry.

`no semantic contract` falling by 171 alongside is the same declarations: a
caller refuses for want of a contract its callee never produced, so freeing the
callee frees the caller in the same pass.

## The trap

**A bucket of 167 with one cause is as likely as a bucket of 167 with ten.**
The previous entry's correction was that a bucket is not one defect; the
correction to the correction is that it is not necessarily many either. Neither
shape is inferable from the count. What distinguishes them is reducing a member,
which cost four programs here and would have cost four programs there.

The generalisation that actually held across both entries is narrower than
either: **the refusal message is the wrong place to reason from.** Both causes
printed the identical empty-spelling line; they had nothing else in common. A
message groups declarations by which check they failed, and a check is the last
thing they share, not the first.

## The remaining 59

All `$iife`, and the improved refusal message named their cause on sight:
`unresolved result type: declared `fn``. Four lines reproduce it.

```fern
function main(): i32 {
    var f: (i32) => i32 = if (true) { ((x: i32) => x) } else { ((y: i32) => y + 1) };
    return f(42);
}
```

`irlower.hoist_value_iife` declares the hoisted IIFE `ret_type: "fn"`
deliberately — the hoist fires only when the arms yield a fn value, so the
function IS a higher-order factory and `closure_ret_fns_of` is gated on exactly
that declaration. The coarse tag is right. What it has no sidecar pair for is
the contract: it copies `lam.ret_fn_ret` / `lam.ret_fn_param_types`, and the
if-expression's IIFE is built by `e_lambda_origin`, which writes the empty pair.

The contract exists at the parse site. `if_expr_rt` sees the arm lambda's own
`params` and `ret_type` and could name `(i32) => i32` outright; it currently
falls to its `"i32"` default for a lambda arm, which is the same shape of wrong
tag as `"bool"` was, one arm over. Carrying it means `IfChain` and
`e_lambda_origin` growing the pair the way `ExprLambda` just did. That is the
next increment, and this time the mechanism is identified rather than guessed at
from a count — which is the whole point of making the refusal name its input.
