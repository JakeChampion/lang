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

**The refusal named nothing.** `unresolved result type: ` printed
`typeinfo.spelling(s.result)`, and an unresolved type spells empty. The message
that would have ended this in a minute — `unresolved result type: bool` — was
the one message it could not print, because a type that failed to resolve has no
spelling to report.

That one is fixed here too, because it is the reason the other three mattered:
167 refusals whose cause was a single word read identically to refusals whose
cause was something else. `unresolved_result_spelling` falls back to the
DECLARED spelling when the resolved type has none.

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

All `$iife`, and the improved refusal message named their cause on sight —
it reads `unresolved result type: declared` followed by `fn` in backticks. Four
lines reproduce it.

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

Half of the contract exists at the parse site and half does not, and which half
is missing decides what the next increment is. The arm lambda's PARAMETER
spellings are always written — `((x: i32) => …)` names `i32` whether or not the
lambda is annotated. Its RESULT is written only when the author annotates it:
`((x: i32): i32 => x)` names one, `((x: i32) => x)` does not, and fernsmith
writes the second. Annotating both arms by hand changes the refusal not at all,
so the plumbing gap is real on its own — `if_expr_rt` falls to its `"i32"`
default for a lambda arm, which is the same shape of wrong tag as `"bool"` was,
one arm over, and `IfChain` / `e_lambda_origin` have no pair to carry even when
one exists.

But plumbing alone reaches only the annotated arm. The unannotated one needs the
result INFERRED from the lambda's body, and inferring a tag is what produced
both bugs this pair of entries is about. So the next increment is the plumbing,
measured on its own, before any inference is layered on top of it — not the two
together, where a wrong guess would again be indistinguishable from a missing
mechanism.
