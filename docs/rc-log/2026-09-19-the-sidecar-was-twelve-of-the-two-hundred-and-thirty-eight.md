# 2026-09-19 — the sidecar was 12 of the 238

`2026-09-19-what-the-typed-path-still-refuses-is-lambdas.md` root-caused the
largest refusal bucket to a missing `ast.ExprLambda` sidecar and said, of the
238 `unresolved result type` refusals, "**that is the whole of the 238**".

It is 12 of them.

## What the fix is

The mechanism that entry describes is real and the fix is correct. `ExprLambda`
now carries `ret_fn_ret` / `ret_fn_param_types` like the four other members of
the #5986 family, `e_lambda_fn` hands them to the nested-function desugar from
the `FuncDecl` that already had them, and all five lambda-to-`FuncDecl` hoists
copy them instead of writing the empty pair.

Only the desugar path is wired. A lambda the source WRITES with a
function-returning annotation still loses its parameter list: the lambda parse
reads `parse_type_name`, which yields `fn_ret` and no `fn_params`, where the
declaration parse reads `parse_decl_type` and gets both. Half a signature is
worse than none — `fn_tag_spelling` would rebuild `() => R` for a function that
takes arguments — so that path is left alone rather than half-filled.

## What it bought

Re-censusing the same 512 fernsmith programs:

| | before | after |
|---|---|---|
| produced whole | 284 | **285** |
| `unresolved result type` | 238 | **226** |
| `no semantic contract` | 207 | 208 |
| `function address is not a closure value` | 27 | 30 |
| verifier: `function value is not an element` | 40 | 42 |

**+1 module.** The 12 refusals it resolves do not free their modules, because
the declarations now reach the NEXT check and fail there instead — which is why
three buckets went UP while the total fell by 3.

The reproducer is the clearest case. The four-line program the previous entry
reduced now gets past the result type and refuses with
`function address is not a closure value`, still `0 of 3`.

## The two claims that were wrong

**"That is the whole of the 238."** The remaining 226 still report an EMPTY
result spelling, on `__lam_N` (167) and `main$…` (49) owners. Whatever leaves
those unresolved is not the sidecar. One probe at one refusal identified one
cause and I generalised it to a bucket that shares a message, which is the same
shape of error as reading a histogram for a ranking.

**The 157 modules.** That figure was the count of modules refusing for
lambda-related reasons ONLY — the whole lambda gap. The previous entry put it
next to the largest bucket and let it read as what that bucket was worth. It is
not: the buckets are stages of one pipeline, not independent populations, and
a module is freed only when its LAST refusal goes.

## The trap

**A refusal count is not a queue of independent defects.** Fixing the first
check a declaration fails moves it to the second, so a bucket can empty by 12
and free one module while three other buckets grow. The honest unit is modules
produced whole, and the only way to know what a fix buys is to re-run the
census — which the previous entry said to do and then, in the same breath,
predicted the answer for.

Worth keeping for whoever takes the next stage: `function address is not a
closure value` is now the reproducer's refusal, and the three buckets that grew
are where the freed declarations went. The next measurement is theirs, not a
prediction from this one.
