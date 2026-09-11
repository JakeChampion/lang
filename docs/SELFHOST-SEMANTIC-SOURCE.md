# Checked source to typed semantic values

Part of #8920's self-hosted typed-IR ownership migration, the "typed frontend
import" step of the [cutover plan](SELFHOST-TYPED-OWNERSHIP-CUTOVER.md).
`examples/self_host/semsource.fern` produces an `ssasem.Func` from a checked
`parser.FuncDecl` and the checker's function scope. It is the first
self-hosted producer of the pre-RC representation; until it existed every
`ssasem` graph was hand-built in a test.

## Boundary

Types come from the checker, not from syntax: `checker.expr_type` gives an
expression's resolved type in a scope, and `checker.scope_type` a binding's
type after `checker.bind_stmt`. Both are read-only views added for this phase.
A binding is a place named by source; the value it holds is an SSA id with an
exact `typeinfo.Type`. Nothing about ownership is read from the AST: parameter
modes come from the declaration (`own` reference is counted, any other
reference is borrowed, scalars are values), and every later decision belongs to
`ssaunits`.

Every produced graph is re-verified by `ssasem.analyze` before it leaves the
producer, so a producer mistake is an explicit refusal, never a partial graph.
Unsupported constructs refuse the whole function with a reason.

## Supported surface

- i32 and boolean literals; locals typed by the checker's post-declaration
  scope, with the initializer's semantic value required to carry exactly that
  type; replacement; shadowing across nested scopes.
- Array and tuple literals as `array_new` / `tuple_new`. An empty literal takes
  its type from the binding or enclosing container it initializes.
- Index and tuple-field projections as `array_get` / `tuple_get`.
- Record literals as `record_new` and named-field projections as
  `record_get`. A record is a declared struct with no type parameters and no
  enum owner; its schema (`semrecords.Record`) is the declaration's field
  names and checked field types in declaration order, which is the order a
  literal names them in. Every record a function's values, contracts or a
  schema's own fields name is entered into the function's schema table, so
  a nested record is walked through its schema, never through syntax. A
  functional update (`T { ...base, f: v }`) reads each field it does not
  replace off the base as a `record_get`, in the order `semir.build_record`
  uses: the base, then the replaced fields in written order, then the
  projections. The plain literal's declaration order is enforced HERE, by
  refusing a literal whose names do not match the schema in order — neither
  checker requires it, and that refusal is what makes the positional
  construction sound.
- String literals as the SSA `const_str` constant, string `+` as a fresh
  concatenation and string `==` / `!=`, typed by `ssasem.binary_result`
  beside the scalar rules. A string constant and a concatenation are units
  of the function's own, released when dead; a literal's static box is
  immortal to the runtime, so its release is a no-op.
- The i32 integer operators — arithmetic, division and remainder, the bitwise
  three, and both shifts — plus i32 comparisons, boolean `==` / `!=`, unary
  `-` and `!`. These are new semantic kinds (the SSA `binary` / `unary` tags)
  whose typing lives in `ssasem.binary_result` / `unary_result`; `ssarc`
  lowers them to the stack IR's `add` … `ne` and `not` / zero-minus. Every one
  is total, because Fern pins the integer edges rather than trapping
  (`docs/INTEGER-SEMANTICS.md`): `x / 0` is 0, `x % 0` is x, `INT_MIN / -1`
  wraps, and a shift count is masked to the operand width. So none of them
  needs a guard, a branch or an abort path here.
- `&&` and `||` as control flow: the right operand runs on its own edge and the
  result is a boolean phi.
- `if` / `else` with binding joins, `while` and `loop` with unlabelled `break`
  and `continue`, `return`. Loop headers get one phi per visible binding
  before the body is known; trivial phis are removed and values renumbered
  densely afterwards, parameters keeping their declared positions.
- Calls of module functions, in expression or statement position, as the
  semantic `call` kind (below).
- Enum values. A variant literal is `variant_new` named by its variant; a
  `match` arm's test is `variant_is` and each named payload position a
  `variant_get` projection of the scrutinee. An enum's schema
  (`semrecords.Enum`) is its union identity and every variant's field shape,
  entered into the function's schema table beside the record schemas. Arms
  are tested in declaration order and each test's false edge enters the next,
  so an arm after a wildcard is unreachable and is not produced; a scrutinee
  no arm matches falls through to the join. A guard, a qualified pattern
  (`Shape.Dot`), a nested, tuple, struct-field, literal or `@` pattern, and a
  non-enum scrutinee are refused.

- `for` over an array as an index loop whose advance runs at the TOP of the
  header with the index starting one before the first element, because
  `continue` branches to the header and a bottom advance would be skipped
  (#2788). The element is an `array_get`, so it is already a borrow anchored
  to its container and needs no new ownership rule.
- The array and string builtins `.len()` and `.append()`, and
  `slice_unchecked` on a string. A slice owns its box and borrows the
  source's bytes, so it is a projection anchored to its source and is
  released by the view helper rather than the ordinary string free.
- Indexing a string, as one byte handed back in an i32. The receiver is read
  the way a length's is and the result owns nothing, so — unlike the slice
  beside it — this is NOT a projection: the byte outlives the string it came
  from, and nothing has to keep the source alive for it.

Refused, each with its own reason: calls of the remaining builtins, local
function values and void functions, floats in expressions, integer widths
other than i32, casts, string ordering, generic records,
destructuring, labelled loops, match guards and the pattern shapes above,
`defer`, closures, receiver methods, generics, external and async functions,
and a value-returning body that falls through.

## Calls

A call is verified against its callee's declared contract, never its body.
`ssasem.Contract` holds the exact parameter types, one unit mode per
parameter (value, borrow or counted, as the callee's own production reads
them from the declaration) and the result type. `build_module` derives one
contract per declaration this boundary can produce and hands the table to
every function; `ssasem.analyze` checks each call's argument and result types
against it, and the unit planner treats a counted parameter like a
construction operand (retained, or moved at the argument's last use), a
borrowed parameter as a read, and a reference result as a fresh unit of the
caller's own. A discarded call result is released at the call.

The module is closed: a produced function whose callee was refused is refused
in turn, transitively, because a contract is only honoured by a body verified
against it. The AST-lowered `main` still calls produced functions with scalar
arguments only, so the caller-side finding below is unchanged.

## Physical loops

`ssarc.lower` previously refused any cyclic graph. `ssalayout.fern` now
computes a structured layout: natural loops from the dominator relation
(`ssadeps.dominates`), each loop a contiguous span headed by its header,
nested loops contiguous inside their parent, everything else in forward
order. `ssarc` emits a region per loop — a `loop` label, then one `block` per
position inside that no nested loop owns, closed just before its position —
and resolves every branch through an explicit label stack, `if` arms
included. A cycle without a single dominating header (an irreducible graph)
is refused as "physical RC needs reducible graph"; the rejection test builds
one by hand.

## Validation

`internal/e2eselfhost/self_host_semsource_test.go`:

- `TestSelfHostSemanticSourcePrint` builds a driver from the self-host tree
  and pins the produced graphs, value types, parameter modes and refusal
  reasons for a fixture module against `testdata/semsource_print.golden`.
  Every produced function also passes `ssaunits.plan`.
- `TestSelfHostSemanticSourceRC` produces every non-`main` function of a
  program, plans and physically lowers each, substitutes the results into the
  production lowering cache next to the AST-lowered `main`, and runs the
  program on arm64, x86-64, x86-64 under the sanitizer and wasm. Output is
  compared exactly and the native runs must balance under `FERN_LEAKCHECK`.
  The fixtures cover loops with `break` / `continue`, nested loops, tuple
  replacement across a branch, projected returns, short-circuit conditions,
  and calls between produced functions: borrowed and counted array
  arguments, a counted argument retained across a call and moved at its
  last use, a temporary result moved into a counted parameter, a discarded
  result, a returned parameter, recursion, a call result carried into a
  loop header, the AST-lowered `main` binding, discarding and projecting
  tuple and array results under `ssarc.caller_sigs`, and the enum shapes
  above.

```sh
go test ./internal/e2eselfhost -run 'TestSelfHostSemanticSource' -count=1
go test ./internal/e2eselfhost -run 'TestSelfHostSSAPhysicalRC|TestSelfHostSSAUnits|TestSelfHostSSASemantic|TestSelfHostSSADependencyVerification|TestSelfHostSSALifetime' -count=1
```

## The caller contract at the AST boundary

Substituting a verified callee under an AST-lowered caller exposed the
mismatch the [physical RC notes](SELFHOST-PHYSICAL-RC.md) predicted. A caller
binding a returned `(i32, i32[])` deep-released the tuple's array only when
the callee's body returned a tuple literal of literals; for a returned local
tuple, or a literal carrying a local array, the caller's syntactic reading of
the callee decided the child was not its own and leaked it, and a projected
call temporary (`carry(k)[0]`) leaked its array the same way. The verified
callee is balanced either way, and the pure AST route leaks the same shapes
without any substitution (#9004).

`ssarc.caller_sigs` closes the gap for produced callees: it rewrites the AST
registries an AST caller reads from the callee's verified result type rather
than its syntax. The contract every produced function honours is one counted
reference whose container owns its children, so a tuple result earns the
fresh-tuple row with a deep-drop flag at every scalar-element array
position, and a scalar-element array result earns the owned-array row. Rows
the caller's syntactic reading recorded for the same result are replaced;
positions those registries cannot release (a string or record element) keep
the AST caller's leak-mode floor, and the rows are only derived for callees
actually lowered here, since the contract needs both sides. The executable
fixture's `main` now receives the three #9004 shapes and balances; removing
the contract feed leaks five blocks on the same program.

## Remaining

The producer does not yet admit casts, integer widths other than i32, floats,
the remaining builtins, destructuring, closures or generics, so no production
consumer is switched and no AST ownership analysis is deleted.

Records, strings, enums and struct-unions cross the boundary (`make`, `wrap`,
`unwrap`, `tally`, `greet`, `shape`, `measure`, `sum_shapes`, `consume`,
`boxed_shape`, `hold`, `mk_node`, `node_size` and `node_sum` in the executable
fixture: a record with a string field and a nested record, an `own` record
parameter, string concatenation in a loop, string equality, a four-variant
enum matched in a loop and carried across iterations, an `own` enum parameter
consumed through `if let`, produced-call temporaries handed to a counted
parameter, a record with an enum field, and a struct-union widened from both
members, matched, and carried across a loop as a phi; balanced on every
target). The AST-lowered `main` receives tuple and array results by contract.
String and record positions of a received tuple, and record, enum and union
results, still rely on the AST caller's own syntactic rows.

Measured against the whole loaded self-hosted compiler, 2,785 of its 7,406
functions produce, plan and physically lower.

Read that number, not the raw refusal histogram: 2,823 of the 4,455 refusals
are `call target was refused` cascades, so the histogram is dominated by
functions whose only problem is a refused callee. What is worth counting is
the LEAF — the first refusal that is not a cascade. The leaves are now led by
string indexing (507, the whole of `array projection contract`), then callees
with no semantic contract, unsupported literals, destructuring, record
literals the schema does not match in order, and the operators that remain:
mixed widths and string ordering. A further 165 functions produce and plan
but are refused by the unit planner for an `.append` whose receiver is not
moved, and 1 by physical RC lowering for an `i64` array element.

The leaf is what to probe against before building: measured deltas have
repeatedly disagreed with the histogram, most sharply on string indexing,
which is the largest leaf and was worth +0 until enough of its callers
lowered.

Next, by measured leaf rather than by histogram: the `.append` receiver gate,
which needs a clone form for a receiver the planner does not move (the
`op_arr_slice` shape `irlower.lower_arr_append_value` already uses) and
carries a real cost — the dominant refused shape is a borrowed-parameter
accumulator in a loop, where a clone is O(n^2) bytes and native escapes it
with an exemption this vocabulary has no analogue for. Then the wider scalar
types, which string indexing, the casts, the mixed-width operators and the
last physical-RC refusal all wait behind. Then a production consumer that
lowers produced functions through this pipeline and feeds `caller_sigs` to
the remaining AST callers.
