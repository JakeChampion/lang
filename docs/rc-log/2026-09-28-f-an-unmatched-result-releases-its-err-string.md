# 2026-09-28 — an unmatched Result releases its Err string (#10439)

AST lowering (`FERN_SEM_IR=`) only. The typed lowering already balanced every
program here.

## Cause

An unmatched, never-reassigned Option/Result local whose payload is a string is
the "OPTSTR:" class, released at exit (or at a loop re-declaration) by
`emit_optstr_deep_free`. Two shapes fell outside that release.

- **`Result[string, string]` built with `Err(fresh)`.** The release freed the
  payload only on the Ok tag, so the Err string leaked. OPTARR already had an
  Err-string sub-tag for this ("OPTARRERR:"); OPTSTR had none.
- **`Result[<scalar>, string]`.** No class claimed it. `unmatched_optstr_ann_is`
  wants a string Ok payload, and the box-only classes refuse a string payload
  spelling. The box and the Err string both leaked, even for an unused local.

## Change

All in `examples/self_host/irlower.fern`.

- `collect_unmatched_optstr_names` also admits a `Result[<scalar>, string]`
  local whose initialiser builds a fresh box: a direct `Ok(..)` / `Err(..)`, or
  an OPTFRESH producer call (`fresh_result_box_init`).
- `optstr_payload_subtags` grants two sub-tags on the "OPTSTR:" site:
  "OPTSTRSCOK:" for a scalar Ok payload, and "OPTSTRERR:" when
  `unmatched_opt_err_str_fresh` proves the Err string fresh. That proof is
  `unmatched_optarr_err_str_fresh` renamed, since OPTARR and OPTSTR now share
  it.
- `emit_optstr_deep_free` reads the sub-tags. It frees the Ok string unless the
  Ok payload is scalar, and the Err string when it is proven fresh. It still
  releases the box on every path.

## A use-after-free on main, found on the way

`or` returns `Ok(x)` around the receiver's own Ok string without taking a count
of it, and `and` does the same with the Err string. So in

```fern
let s: Result[string, string] = Err("x");
while (i < 3) {
    let r: Result[string, string] = Ok((i + 100).to_string());
    if (i == 0) { s = r.or(Err("z")); }
    i = i + 1;
}
```

`r`'s re-declaration freed the string `s` still holds. On main the wasm build
answers 20 instead of 23. The x86-64 sanitizer does not see it: a string that
short is stored inline on x86-64, so its free touches no heap block. The Err
release above would have extended the same hole to `and`.

The escape gate reads a method receiver as a borrow. The collector now also
refuses a local when some method call on it has its result bound, stored,
returned or passed to a non-borrowing parameter (`method_result_moved_stmts`,
which now treats an empty method name as "any method"). A result read through
in the same expression, such as `q.unwrap_or("xyz").len()`, is still admitted.

## Measured (`FERN_LEAKCHECK=1`, AST lowering, allocs / frees)

| row | before | after |
|---|---|---|
| `string_ok` (the issue's first program) | 300 / 200 | 300 / 300 |
| `scalar_ok` (the issue's second program) | 300 / 100 | 300 / 300 |
| `unused` | 1 / 0 | 1 / 1 |
| `bool_ok`, `scalar_ok_built_ok`, `lent` | leak | balanced |
| `producer` | 200 / 150 | 200 / 150, pinned: the callee's leak, #10628 |
| `result_outlives`, `ok_result_outlives` (guards) | wasm answers wrong | 12 / 3, pinned, right answer |
| `err_aliases_live` (guard: the Err payload is a live local) | new row | 300 / 200, pinned |

`TestSelfHostResultErrString{X86_64,Arm64,Wasm}` holds every row to the
interpreter's answer under both lowerings. The x86-64 leg also runs
`FERN_SANITIZE=1`. The guard rows run on wasm too, which is where the outliving
string is really freed. In `TestSelfHostResultMethodTParam`, `and_err` moves
from 150 / 0 to 150 / 50. What it still leaks is `and`'s handed-back box
(#10388).

Every Option/Result, matrix and unwrap suite in `internal/e2eselfhost` passes
unchanged.
