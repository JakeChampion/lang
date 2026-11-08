# The checker asks each body once

Two checker passes collected the same facts again for each function body.
This change keeps one answer for each function and gives it to each pass.
The diagnostics and the generated code do not change.

## Last-use facts are decided at the first question

`own_diags` resolved the names of every function and computed its last-use facts before the ownership walk.
Only 1,473 of the 4,843 functions on the fixed checker compile ask a last-use question.
The facts now wait in `OwnCtx.last_use`, a `Cell[LastUses]`, until `lu_has` asks the first question.
`lu_has` then keeps the facts in a `util.NameIndex`, not in a string list that each question scanned.
A nested lambda shares the cell of its function, because the facts already include the lambdas of that function.

## Annotations are collected once for each function

Six sites in `check_module_pass` walked each body to collect its type annotations.
These sites are the arity check, E064, E057, the handle check, the const-kind check and E085.
`module_annots` now collects them once for each function in focus.
Each site reads the list for its function by the function index.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from main at 060d86c4.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| main at 060d86c4 | 30,751,197,104 |
| this change | 30,329,874,673 (−1.37%) |

Both compilers emit byte-identical checker executables.
The `-g` builds of the two compilers attribute the difference as follows:

| function | calls before | Ir before | calls after | Ir after |
| --- | --: | --: | --: | --: |
| `checker.own_diags` | 8 | 2,167 M | 8 | 1,842 M |
| `checker.lu_last_use_args` | 4,843 | 536 M | 1,473 | 311 M |
| `lexical.resolve_func` | 8,328 | 381 M | 4,924 | 278 M |
| `checker.collect_annots` | 23,471 | 117 M | 4,843 | 24 M |

## What the profile shows next

The checker remains the largest module, at 10.6% of self cost.
`check_module_pass` is 17.8% inclusive, and most of it is diagnostic walks that rebuild a scope and type expressions again.
The ownership walk still calls `annotate_func` for each function that returns a pointer and calls a method (561 M).

`lift.lift_lambdas_view_typed` resolves the names of every function (165 M), and its output keeps the resolved names.
The const substitution walks every body twice: once for each module in the parser, and once in `flatten.bundle_with_prefixes` (276 M together).
The generic AST walks in `astwalk` are 5.2% of self cost.
