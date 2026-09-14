# Generics at the semantic boundary

A generic declaration is compiled two ways in this repository, and the two
answer the reference-counting question differently.

`docs/SELFHOST-SEMANTIC-SOURCE.md` holds the census; this file holds the rule.

## The shape

    pub function fold_stmt_nodes[T](st: ast.Stmt, own acc: T,
                                    visit_stmt: (ast.Stmt, own T) => T,
                                    visit_expr: (ast.Expr, own T) => T,
                                    descend: (ast.Expr) => boolean): T

and every one of its callers threads the accumulator by REPLACEMENT:

    acc = visit_stmt(st, acc)

The AST lowering (`irlower.fern`) compiles ONE body for every `T`. An erased
`T` is one machine word — pointer-shaped or i32 — and nothing at run time
tells the two apart, so that body may emit neither retain nor release on it:
`__fern_rc_dec` on an erased word decrements a refcount at one instantiation
and an integer at another. `erased_passthrough_safe` and `erased_widenable`
are the whole of what the backends do with such a word, and the invariant
behind them is that an erased slot is pointer-shaped or i32 and never a bare
wide scalar. That path is unchanged by anything below.

## The rule: the boundary instantiates

The semantic source boundary (`semsource` / `ssasem` / `ssaunits` / `ssarc`)
produces a generic body ONCE PER INSTANTIATION, the way `internal/monomorph`
does on the native pipeline. A declaration that spells type variables is a
TEMPLATE: it has no body of its own, only a contract whose variables are
`typeinfo.TypeErased`. A call site binds them — structurally, left to right
over the arguments, so a function-typed parameter's own variable slots bind
through it, and one variable binds one type for the whole contract — and the
call names an INSTANCE, `fold_stmt_nodes$FwdScan`, whose contract is the
template's with every variable substituted. The instance is a body of its own,
produced under that binding, and the producer runs a worklist over the
instances the bodies request, to a fixpoint.

Inside an instance every type is concrete. `acc: T` at `T = string[]` is a
borrowed `string[]` parameter; at `T = i32` a value. A `T[]` is an array whose
element has a store width and a drop; `(ast.Expr, T, boolean)` a tuple whose
element can be released. So a generic body needs no rule of its own: the
unit planner and the physical lowering see an ordinary function, and every
retain, release and drop is the one that type would get anywhere else.

Two details make one source serve every instance:

- **`own` on a scalar is normalised out of the type** (`typeinfo.own_flags`,
  re-applied on substitution). `(ast.Stmt, own T) => T` at `T = i32` is
  `(ast.Stmt, i32) => i32`, which is the type a scalar visitor actually hands
  out; at `T = string[]` it keeps the `own`, and a lending visitor is refused
  there because the two promise opposite things of the same call.
- **A parameter's mode follows its declaration**, then the instantiation:
  `own acc: T` is counted where `T` binds a reference and a value where it
  binds a scalar (`ssasem.instantiate_contract`). A template's variable
  counts as a reference until an instance says otherwise.

Where it lives:

- `semsource.resolved` reads a type the checker computed for a body and, in a
  template, substitutes each variable from the binding the body is produced
  under. `semsource.contract` keeps a template's variables for the call site.
- `semsource.invoke` binds the arguments, instantiates the contract
  (`ssasem.instantiate_contract`), names the instance (`ssasem.instance_name`:
  the template's key, then the type each variable binds in the contract's own
  order, `$`-separated the way a lifted lambda's symbol is) and REQUESTS it.
  `semsource.build_module` produces every request, then every request those
  bodies make, and reports each template as produced when every instance it
  was asked for produced. A template no produced body reaches is
  "uninstantiated generic".
- A closure body hoisted out of a template copies the template's type-variable
  spellings and declares no type parameter of its own (the arity-keyed
  indirect-call table fixes its ABI), so its constructor instantiates it under
  the binding the enclosing body is produced under.
- A generic METHOD is still refused: its receiver spelling is what the
  checker's dispatch resolves, and the binding here is read off a free
  function's parameters.

The unit planner has nothing to prove about a variable, because it never sees
one: `ssasem.schema_error` refuses a produced graph carrying a `TypeErased`
anywhere, as an unresolved type.

## What it costs, and what it does not

An instance is a body: a template reached at eight types is emitted eight
times. That is the cost native already pays for the same declarations, and the
one the AST lowering avoids by erasing. Nothing is boxed, no scalar crossing a
generic boundary allocates, and the indirect-call table keeps its arity key —
which is what ruled out the two runtime answers this note used to keep on the
table: boxing every erased value (the Perceus answer, at the price of an
allocation per scalar crossing) and passing a drop function beside each
erased word (a second word in every generic's ABI, and so in the table's key).
Both existed to let ONE body release a value whose type it could not see; a
body per instantiation sees it.

The AST lowering and the boundary therefore disagree on the symbol a generic's
body has — `util.append_all` erased against `util.append_all$arr$string` — and
the RC execution leg mixes the two on purpose: main is AST-lowered and calls
the erased body, the produced callers call the instances, and both link. A
production consumer that lowers every produced function through this pipeline
emits the instances and no erased body at all.
