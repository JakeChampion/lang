# A generic enum keeps its methods through monomorphisation

2026-09-22. `parser.monomorphize_enums` clones a generic enum's methods per
instantiation, and a builtin union's literal settles its own type arguments
from its payload. Three corpus cases move to the typed path.

## What it was

The enum pass skipped any generic enum that declared a method or a derive:
`enum_has_method` gated every predicate in it, so `Opt[i32]` with an
`unwrap_or` stayed the erased `Opt`, and `semsource.enum_entry` refused every
declaration that touched it (`variant field type`, `return type: declared
i32, returns`). Three conformance cases sat on the AST lowering for this alone:
`method_type_arg_names_the_method` (55 declarations),
`generic_receiver_methods` (5) and `generic_array_methods` (7). The struct pass
had cloned a generic struct's methods since it was written; the enum pass had
the comment saying it did not.

## What it does

`clone_enum_method` is `clone_struct_method`'s shape: substitute the enum's
variables in the signature, re-point the receiver at the clone, rewrite the
body under an environment that types the receiver as the instantiation so
`me_stmts` mangles its constructions and arms. A derived method's bare `Opt`
receiver and parameters read as the instantiation being cloned
(`enum_self_ty`); the variables are the receiver's own spelling when it has
one. A method with a type parameter of its own — `pair[U](other: Box[U, E])`
— folds into the free generic `__smm_Box_pair` before any of this, exactly as
a struct's does; `is_generic_method_own_tps` admits an enum receiver now.

An enum used only at a composite key (`Opt[(i32, i32)]`) is never
instantiated by the pass and keeps its declaration, so it has to keep its
methods too: phase 1 holds every generic enum's methods aside, rewritten as
any function is, and phase 3 gives back those whose enum was not dropped. A
held method's own `Opt[T]` spelling would otherwise key a clone `Opt__T` and
drop the enum from under that composite-key use, so `genum_key_from_anno`
refuses a key naming one of the enum's own type parameters.

That fold left one gap: `b.pair(Full(9))` has to settle `U` from `Full(9)`,
and `mono_infer` has no type for a bare variant construction. `variant_arg_binds`
binds the enum's variables through the variant's field types against the
payload's inferred types, and carries each to the parameter's argument in the
same position. The written type-argument list is erased by the parser, so
this inference is the whole of it for `.pair[i32](...)` too.

The last refusal on the 55-declaration case was in the typed producer, not
the parser, and predates the change: `r.and(Ok(3))` on `Result[i32, string]`.
`and[U](other: Result[U, E])` binds U from that argument, so `invoke_rest`
hands the literal `Result[U, string]` as its destination and `variant`
refused a whole that is not concrete. `settled_union` replaces the `Some`-only
special case (`some_of`): each payload's checked type binds the variable its
field spells, for Ok and Err as for Some, and a `Some` with no destination at
all is still the Option of its payload. The checker records a bare `Result`
for the literal on `and` and `or` alike — it never types an argument against
its parameter — so the producer, which holds the parameter, is where the
settling belongs.

`genum_is_mono` and `enum_has_method` are gone, and with them the `funcs`
argument the pass threaded through seven functions only to ask the question
(47 call sites).

## Measured

x86-64, arm64 and wasm, typed path, produced whole, exit as expected:

| case | declarations | exit |
| --- | ---: | ---: |
| `method_type_arg_names_the_method` | 55 of 55, 2 instances | 23 |
| `generic_receiver_methods` | 5 of 5 | 84 |
| `generic_array_methods` | 7 of 7 | 24 |

Corpus census, x86-64, against the compiler carrying #9996:

| | before | after |
| --- | ---: | ---: |
| cases produced whole | 512 of 597 | **515 of 597** |
| declarations produced | 18,053 of 18,399 | **18,121 of 18,400** |

Exactly the three files move; the one other row that changes,
`audit_std_string` 121 to 122, is a stdlib declaration main added since the
baseline. The 13 cases still on the AST lowering are unchanged, and none of
their leaves is this one.

Two production rows with an absolute leak pin on the sanitizer leg and the
wasm leg (`TestSelfHostSemanticProduction`):
`generic-enum-methods-clone-per-instantiation` — a `@derive(cmp.Eq)` generic
enum over a string payload, a method, a method with its own parameter and
`==` through the derived clone, 200 rounds, produced 110 of 110 — and
`builtin-union-payload-settles-the-literal` — `Result.and(Ok(i + 1))` on Ok
and Err receivers, 200 rounds, 51 of 51, 1000 allocs and 1000 frees where the
AST lowering frees none. Both answer what the AST lowering and the interpreter
answer. `TestSelfHostGenericEnumIR{X86_64,WasmIR}` carry the two
new monomorphiser shapes under the size bound that proves the IR route, and
`TestSelfHostBuiltinUnionPayload{X86_64,Arm64,Wasm}` pin the string payload
through `Result.and` and `Option.and` under the churn gate, 1000 rounds flat,
with the interpreter as the oracle.

## Traps

The AST lowering, the production test's oracle, misreads a string payload
through `and`'s erased `U`: `r.and(Ok("vw"))` exits 128 on x86-64 and 0 on
arm64 where native, the interpreter and wasm say 2, and the answer moves with
the allocator (#10014). A row over that shape has no AST answer to pin, so
the row's payload is an i32, which the AST lowering answers correctly while
still freeing nothing.

A unit variant cannot settle a method's own parameter: `s.swap(Nn)` is E040 on
native, and the self-host reports it as a module that is not IR-eligible rather
than the diagnostic — a checker divergence, not a lowering one, and not fixed
here. Write the test program with a payload.

The self-host build under `bin/fern-selfhost` is what the census script runs;
rebuilding it mid-census splits the run across two compilers. Rebuild first,
then census.
