# Diagnostics

Status: normative index. Every rejection the front-end can report has a
stable code, an explanation (`fern explain E0NN`, sourced from
`internal/diag/explanations/`), and — where one exists — a conformance
case that pins it.

The codes are the closest thing Fern has to a written statement of its
static semantics. The *rules* they enforce still live only in
`internal/checker`; this index says which rules exist and which are
pinned by something an independent implementation could be measured
against, not what each rule is.

## Why conformance coverage, when the Go tests already pass

Almost every code here is exercised by a Go test under
`internal/checker` or `internal/parser`. That is good coverage of
`internal/`, and **none of it survives the native freeze.**

`docs/NATIVE-CONVERGENCE.md` makes the self-host compiler the
definition once the freeze preconditions (#4451) go green. A Go test
cannot measure the self-host compiler; a conformance case can, because
the corpus is source plus expected output and any implementation can be
run against it. So a diagnostic rule with only Go-side coverage is a
rule that stops being checked at exactly the moment it starts mattering
most.

**58 of 75** codes are pinned by a conformance case. The table
below is verified against reality by `TestDiagnosticsIndexIsAccurate`:
a code with no explanation, an explanation with no row, a claimed case
that does not exist or does not actually emit the code, and a `—` on a
code some case *does* emit are all failures. The count in this paragraph
is checked too, so improving or regressing coverage has to be a visible
edit.

## The dead entry this found

`E039` documented a bare `len(x)` builtin with its own arity and
argument-type errors. No `errfCode` site emits it, and `len` is a
method — `xs.len()` — so the example in its own explanation could not
compile, let alone produce E039. `docs/SELFHOST-CHECKER-PORT.md` had
already noticed the code was dead without anyone removing the
explanation, so `fern explain E039` went on describing a construct the
language does not have. It is deleted.

## Codes

| Code | Rule | Pinned by |
| --- | --- | --- |
| `E001` | Undefined identifier | `err_undefined_ident` |
| `E002` | Type mismatch | `err_return_type` |
| `E003` | Cannot assign value to variable of different type | `err_assign_type` |
| `E004` | Wrong number of arguments to a call | `err_arity` |
| `E005` | Struct literal missing field | `err_missing_field` |
| `E006` | Top-level declaration redeclared | `diag_e006` |
| `E007` | Duplicate field | `err_duplicate_field` |
| `E008` | Non-boolean condition | `err_if_not_bool` |
| `E009` | Operator requires integer-compatible operands | `err_operator_type` |
| `E010` | Reserved built-in name | `diag_e010` |
| `E011` | `break` or `continue` outside a loop | `diag_e011` |
| `E012` | `return` without value in a non-void function | `diag_e012` |
| `E013` | Variable already declared in this scope | `err_redeclare` |
| `E014` | Variant not part of enum | `diag_e014` |
| `E015` | Variant payload count mismatch | `diag_e015` |
| `E016` | Union declaration error | `diag_e016` |
| `E017` | Duplicate enum variant | `diag_e017` |
| `E018` | Duplicate function parameter | `diag_e018` |
| `E019` | Generic type-parameter count mismatch | `diag_e019` |
| `E020` | Empty array literal needs a type annotation | `diag_e020` |
| `E021` | Invalid method receiver / trait conformance | `diag_e021` |
| `E022` | `let-else` / `if-let` source error | — |
| `E023` | Unknown enum | — |
| `E024` | Tuple destructure error | `param_destructure_err_arity` |
| `E026` | Wildcard `_` arm must be last | — |
| `E027` | Match guard must be boolean | — |
| `E028` | Match arm variant already covered | — |
| `E029` | Variant pattern qualifier mismatch | `diag_e029` |
| `E030` | `match` is not exhaustive | `err_nonexhaustive_match` |
| `E031` | Match arms have incompatible types | `diag_e031` |
| `E032` | `use` clause inference error | — |
| `E033` | Unsupported cast | `diag_e033` |
| `E034` | Array / indexing error | `diag_e034` |
| `E035` | Literal pattern error in match | `tuple_match_err_arity` |
| `E036` | Variant constructor call error | `diag_e036` |
| `E037` | Slice error | `diag_e037` |
| `E038` | Call error | `err_arg_type` |
| `E040` | Generic type-parameter inference / arity error | — |
| `E041` | Operator error | `diag_e041` |
| `E042` | `?` operator error | `diag_e042` |
| `E043` | Struct literal field error | `err_unknown_field` |
| `E044` | Captured variable has unsupported type | — |
| `E045` | Map key / value type error | `err_map_key_no_hash` |
| `E046` | Tuple field access error | `diag_e046` |
| `E047` | Integer literal out of range | `diag_e047` |
| `E048` | Field is immutable after construction | `compound_field_assign` |
| `E049` | Reference-typed captured variable is read-only | `diag_e049` |
| `E050` | Use of an owned parameter after it was consumed | `diag_e050` |
| `E051` | Argument to an owned parameter must be an owned value | `diag_e051` |
| `E052` | missing return in a value-returning function | `diag_e052` |
| `E053` | `fip` / `fbip` function violates its shape rules | `diag_e053` |
| `E054` | an `@export` function cannot be generic | — |
| `E055` | the result of a collection operation is unused | `diag_e055` |
| `E056` | cannot assign to an array element | `diag_e056` |
| `E057` | `Cell[T]` element type is not allowed | — |
| `E058` | labelled `break` / `continue` names no enclosing loop | `diag_e058` |
| `E059` | `as?` downcast requires a `dyn Trait` value | `diag_e059` |
| `E060` | invalid `as?` downcast target | `diag_e060` |
| `E061` | block-expression has no trailing value | `diag_e061` |
| `E062` | ambiguous method on a multi-trait object | — |
| `E063` | returning a `[T]` slice that views function-local storage | `diag_e063` |
| `E064` | unknown type | — |
| `E065` | returning a `str` view of a function-local string | `diag_e065` |
| `E066` | target does not provide a required capability | — |
| `E067` | a `@must_consume` value was not consumed on every path | `diag_e067` |
| `E068` | `fip` / `fbip` function fails IR allocation verification | `diag_e068` |
| `E069` | a 32-bit value was reinterpreted as a pointer-shaped type | — |
| `E070` | a dependency package reaches a capability it was not granted | — |
| `E071` | a literal outside the Unicode scalar range was cast to `char` | — |
| `E072` | ? | — |
| `E073` | an explicit call to a `core/mem.Drop` finalizer | `diag_e073` |
| `P001` | Unexpected token (parse error) | `diag_p001` |
| `P002` | Numeric literal error | `diag_p002` |
| `P003` | Left-hand side of assignment is not assignable | `diag_p003` |
| `P004` | At most one `_` placeholder in a piped call | `diag_p004` |

## The 20 unpinned codes

These have Go-side coverage but no conformance case. Most resisted the
mechanical derivation used for the rest — their catalogue examples are
fragments that need a surrounding program the example does not supply,
or they need a multi-module or capability-manifest setup a single
`main.fern` cannot express (`E066`, `E070`). They are the remaining
work, not a decision.
