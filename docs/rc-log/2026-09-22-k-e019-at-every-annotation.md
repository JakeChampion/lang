# E019 at every annotation position

2026-09-22. `diag_e019` is rejected by the self-host checker, as native
rejects it, and leaves the corpus' partial list.

## What it was

`var p: Pair[i32] = …` against `struct Pair[A, B]` compiled on the
self-host. The checker's arity rule (`type_arity_diag`) ran at parameter and
struct-field annotations only, on the reasoning that a `var` or return
annotation with the wrong arity also trips E003 / E002 in Go and the code
sets should match. The self-host reported neither, so the program reached
the lowering, and the typed producer refused it ("binding declared Pair
holds a semantic value of Pair__i32__i32") while the AST lowering compiled
it. The rejection-gap file listed it as a program the self-host accepts and
native refuses.

An array of a short instantiation (`var xs: Pair[i32][] = []`) was missed at
every position: `count_type_args` answers -1 for an array, which the rule
read as "no argument list".

## What it does

`check_module` runs `type_arity_diag` over each function's return
annotation and every body annotation (`collect_annots`), and the rule peels
array suffixes before counting. `diag_e019` leaves
`testdata/selfhost-rejection-gaps.txt`.

## Measured

`fern -check` on the case reports `5:1: error[E019]: struct Pair has 2 type
parameter(s), 1 supplied`, the line and message native reports. One checker
row (`TestSelfHostCheckerCodes`) pins the array `var`, where native reports
E019 alone. At a plain `var` or a return annotation native adds follow-on
E003 / E002 on the mismatched value, which that exact-code-set table would
demand as well, so those two shapes are pinned by the rejection gate
(`TestSelfHostRejectsConformanceErrorCasesX86_64`, which now requires
`diag_e019` to be rejected) rather than by a codes row. The corpus census's
partial list drops by one; the case now joins the diagnostic cases that
produce nothing.
