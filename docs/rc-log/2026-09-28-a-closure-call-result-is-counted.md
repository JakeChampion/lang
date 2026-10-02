# 2026-09-28 — a closure call result is counted, and a container takes the count (#10437)

AST lowering (`FERN_SEM_IR=`) only. The semantic lowering balanced every shape
here already.

## The cause

A closure box stored in a container is released with the container only when
the container's admission proves the store counted. Each admission accepted a
lambda or its `__mkclo$` box and refused a closure-returning call:
`clofld_scan` for a struct's fn field, `cloarr_elem_is_owned` for an array
literal or `.append`, and the mixed scalar-tuple class for a tuple. The call
was refused because a closure-returning function could hand back a box it did
not own: `return f` of a parameter, `return h.f`, `return fs[i]`, or a `for`
variable went back bare and uncounted. The caller could not tell such a result
from a fresh one.

Other sites already treated the result as owned, so an uncounted return was a
use-after-free, not only a missed release. `let g = pass(keep)` released
`keep`'s box on its loop rebind (#10392's release), and `pass(keep)(10)`
released it through the callee temp.

## What changed

- **A closure return is counted.** `lower_stmt_return` retains a returned
  closure this frame holds no count for (`ret_closure_uncounted`): a non-`own`
  fn parameter, a closure local the exit sweep does not release, a struct's fn
  field, or a closure-array element. Every closure-returning call now hands its
  caller one count.
- **The containers accept the call.** `closure_value_is_counted` (a lambda, a
  `__mkclo$` box, or a call to a `closure_ret_fns_of` member) is the one
  predicate `clofld_scan`, `cloarr_elem_is_owned` and the tuple class read.
  `clo_elem_is_box` is gone.
- **A tuple's closure position is released.** The mixed scalar-tuple class
  (`TUP:` with element kinds) admits a counted closure at a `clo` position and
  records kind `a` for it. The writer agreement `TUPSTRW:` becomes `TUPOWNW:`
  (`assigns_rc_pos_owned`): it now also requires every rebind to store a counted
  closure at each closure position. The escape scan reads `p.0(args)` as a
  borrow.
- **A struct owns a closure local stored in it.** For a type the `clo:`
  admission gives a `k_clo` drop, the literal retains a bare ident fn-field value
  (`clo_field_ident_counted`), and `clofld_scan` admits one. Before this, the
  struct borrowed the local's box: a struct appended to an array, or returned
  from `wrap`, outlived the local and read a freed box. The test program
  returned 99 where the interpreter returns 4. A bare `return b.f` no longer
  refuses the admission, since the return retains it.
- **A rebind releases the old box even when a call hands the same box back.**
  `lower_stmt_var_closure` and the `mk()(args)` callee temp (`$coc`) store with
  `same_dec`. The callee temp used to be released only by the exit sweep, so a
  loop stranded every box but the last.

## Measured (x86-64, `FERN_LEAKCHECK=1`, AST lowering, allocs / frees)

| program | before | after |
|---|---|---|
| #10437 struct field | 12 / 6 | 12 / 12 |
| #10437 `.append` | 8 / 2 | 8 / 8 |
| #10437 array literal | 18 / 6 | 18 / 18 |
| tuple element from a call | 12 / 0 | 12 / 12 |
| tuple element from a local | 18 / 12 | 18 / 18 |
| `let g = pass(keep)` in a loop | 1 / 1, use-after-free | 1 / 1, clean |
| `closureCallIntoContainerSrc` | 78 / 22 | 78 / 78 |
| `closureContainerSharedSrc` | exit 99 (want 4), 52 / 40 | exit 4, 52 / 52 |
| `closureReturnBorrowedSrc` | use-after-free | 6 / 6, clean |

wasm matches x86-64 on every row. `TestSelfHostClosureIntoContainer{X86_64,Arm64,Wasm}`
holds the three programs to the interpreter's answer and a balanced census on
both lowerings, with the sanitizer silent on x86-64.

## Not covered

- A variant payload: an enum with an fn payload is outside
  `enum_all_variants_rc_droppable`, so the box and its closure both leak
  whatever the payload is built from. That is #9841, a separate admission.
- A map value, on both lowerings: a closure-valued map leaks one box per insert
  of a call result on every target, and on x86-64 the map's release never gives
  back a closure value at all, even one inserted from a local.
- A closure call result passed as an argument (`apply(mk(i), 7)`) is not
  released after the call. A closure parameter has no borrowability verdict to
  release it under.
