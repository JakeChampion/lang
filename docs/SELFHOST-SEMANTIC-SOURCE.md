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
- Array and tuple literals as `array_new` / `tuple_new`. A literal takes its
  type from the binding or enclosing container it initializes when that names
  one, so a member widens to the union the tuple or array declares; an empty
  literal has nothing else to name it.
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
  projections. A plain literal names every field exactly once in whatever
  order the source likes, which neither checker constrains, so its fields are
  PLACED by name the way an update's are — evaluated in written order, which
  is where a side effect between two of them would show, and assembled into
  declaration order for the positional construction. Placement is total: as
  many fields as the schema declares, each naming a distinct slot, covers
  every slot exactly once.
- String literals as the SSA `const_str` constant, string `+` as a fresh
  concatenation and string `==` / `!=`, typed by `ssasem.binary_result`
  beside the scalar rules. A string constant and a concatenation are units
  of the function's own, released when dead; a literal's static box is
  immortal to the runtime, so its release is a no-op. `for c in s` over a
  string is one `str_index` read per step, the byte typed u8 as the checker
  types it, with the text borrowed for the loop's span.
- The integer operators — arithmetic, division and remainder, the bitwise
  three, and both shifts — plus integer comparisons, boolean `==` / `!=`, unary
  `-` and `!`. These are new semantic kinds (the SSA `binary` / `unary` tags)
  whose typing lives in `ssasem.binary_result` / `unary_result`; `ssarc`
  lowers them to the stack IR's `add` … `ne` and `not` / zero-minus. Every one
  is total, because Fern pins the integer edges rather than trapping
  (`docs/INTEGER-SEMANTICS.md`): `x / 0` is 0, `x % 0` is x, `INT_MIN / -1`
  wraps, and a shift count is masked to the operand width. So none of them
  needs a guard, a branch or an abort path here.

  Each runs at ONE width: both operands carry the same integer type and so
  does the result. Fern has no implicit numeric CONVERSION, so a byte and an
  i32 cannot meet at all. The checker does auto-widen the narrower side of a
  same-signedness pair (`i64 + i32`), rewriting the operand with a cast, but
  the tree this producer reads is not the rewritten one — so a mixed-width
  operator is refused here rather than promoted on a guess.

- The integer types are i32, u8 and i64. A byte rides the same i32-shaped slot
  on every backend, so it costs no new physical representation — what
  distinguishes it is the range, and `+`, `-`, `*` and `<<` mask their result
  back to it. **So does an i32's**: the stack IR runs the operators in a
  register wider than either type, and without the mask a produced
  `2147483647 + 1` was 2147483648 rather than INT_MIN. The AST lowering has
  emitted that step since #3581; this boundary did not, and the executable
  fixture now pins both widths through a comparison (a printed result is
  truncated on its way out and reads the same either way).

- An i64 gets a slot of its own, which only wasm spells out: in the function's
  type for a parameter and a result (`irlower.result_i64`), and in its locals
  otherwise. Its operators run at width 64 and are already full-width, so
  nothing masks after them; negation pushes its zero at that width too, or the
  subtraction's two sides disagree. A 64-bit literal does not fit the semantic
  constant's i32 immediate at all, so it carries the literal's SOURCE TEXT
  instead — the form the backends splice straight into a 64-bit immediate, and
  the one the AST lowering already uses for the same literal. An integer
  literal TREE — `0 - 1`, `1 << 40` — is the width of its destination, or of
  the operand beside it, before either side is produced, the way the checker
  settles it; without that a 32-bit `0 - 1` fails the exact-type rule at an
  i64 binding.

  It is a VALUE here and a record or variant FIELD, never an array or tuple
  ELEMENT. An array or tuple stores one i32-shaped word per slot —
  `op_arr_make` and `op_arr_get` are emitted at width 32 — so a wide element
  would be written through a narrow store on wasm, and an array or tuple TYPE
  carrying one is refused. A record or variant stores each field at the width
  its declaration names: the construction carries its declaration's index
  (`op_struct_make`), a record's resolved by name and a variant's by name
  within its enum, since two enums may declare one variant name with
  differently sized payloads; the physical lowering takes the declaration
  table for it, as the AST lowering does. A wide operand of a construction
  whose declaration is not in the table is refused rather than built through
  a narrow store; a read takes its offset from the field index alone and its
  width from the schema this boundary verified the projection against.

  u64 and usize are not admitted: they select the unsigned operators, which is
  a second signedness rule and not just a second width.

- A cast between the integer types, or between one of the two signed widths
  and the f64, as the semantic `cast` kind. `e as T` reaches the producer as
  a unary whose operator names T, and the destination is the checker's type
  for the whole expression rather than the spelling. Crossing the 64-bit
  boundary is an explicit extend or wrap; inside the i32 domain a narrowing
  masks and a widening emits nothing, because the narrower type's own
  producing sites keep its value in range. Into the f64 is the signed convert
  at the operand's width and out of it the truncation toward zero at the
  result's — real instructions, which is why the byte, whose convert has no
  opcode at that width, is not offered them. A cast to or from anything else
  — a string, the `as?` downcast — is refused.

- The f64, as a VALUE and a declared field but never an array or tuple element,
  for the same reason the i64 is one (`narrow_slot`): it gets a slot of its
  own that only wasm spells
  out (`irlower.result_f64`, the `f64_slots` a produced body declares). A
  literal is an f64 — the checker types it polymorphic and settles it where it
  lands, and this vocabulary has one float width, so only a `f32` suffix or
  an f32 destination refuses it — and it carries its source text the way a
  wide integer does, for the backends to splice. The four arithmetic
  operators are the stack IR's own float opcodes and never wrap, a comparison
  is a boolean, negation is the sign flip; `%` has no float opcode and the
  bitwise operators no float meaning, so both refuse. An operator decides its
  width the way the integer ones do — from the checker's type for the whole
  expression, or from whichever operand bears one — so a literal on either
  side takes it. The 32-bit float has no representation here.

- Integer literals in both bases the lexer writes, decimal and hexadecimal.
  A suffix names the type outright, which is how a byte literal (`b'x'`)
  carries its width; an unsuffixed literal has none of its own and takes the
  integer type of what it is written against, so `var b: u8 = 65` binds a byte
  and `65` in an i32 position an i32. For an operator, that width is decided
  BEFORE either operand is produced — from the checker's type for the whole
  expression, or from whichever operand bears one — so a literal takes it on
  either side. The value is `util.lit_to_i32`'s, the same one the production
  lowering reads, and a constant outside its type's range is refused rather
  than wrapped.
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
  to its container and needs no new ownership rule. It is also a checker
  BINDING for the body's span — `checker.for_body_scope`, the loop's analogue
  of `arm_scope`, typed the way `check_stmt` types it — because every type
  this boundary takes from the checker is read in the scope it carries: a
  literal's own type, an unannotated binding's, an operator's width. Without
  the binding an expression naming the element is an undefined identifier,
  which collapses the literal or binding AROUND it to unknown and refuses the
  whole function for a construct it supports.
- A call in statement position may return nothing: its value is void, no
  name binds it, and the physical call stores the dummy every void callee
  pushes for a statement-level drop. In expression position a void result
  is refused, since the checker types nothing by it.
- The runtime builtins `print`, `eprint`, `strbuf_append`, `strbuf_reset`,
  `strbuf_take` and `__memchr`, each with a contract of its own in the table
  a module's declarations fill: the writers and the builder's append read
  the string they are lent and copy its bytes, reset and take own no
  argument, and the byte search reads its string. Physically each is its
  own stack IR op rather than a call, `print` the payload then the newline
  as the AST lowering writes it.
- The array and string builtins `.len()`, `.append()` and `.with()`, and
  `slice_unchecked` on a string. Both array builtins take ONE unit of the
  receiver and hand one back, and both reach the same count test: the
  receiver's own box when its unit is the only one that box's count names,
  and a fresh copy of it otherwise — the AST lowering's two forms, chosen at
  run time rather than by the receiver's syntax. The test belongs to this
  lowering rather than to the shared runtime push, which gives the unit back
  only on the sole-owner side. So the receiver need not be one the planner
  MOVES: a receiver this function does not own is retained at the call, the
  count then names two boxes, and the copy runs. A counted element type
  retains the copy's elements, and `.with` releases the element its store
  replaces. A slice owns its box and borrows the source's bytes, so it is a
  projection anchored to its source and is released by the view helper
  rather than the ordinary string free.
- Indexing a string, as one byte handed back in a u8 — the type the checker
  gives the expression, so a binding or an operator over it needs no
  reconciliation. The receiver is read the way a length's is and the result
  owns nothing, so — unlike the slice beside it — this is NOT a projection:
  the byte outlives the string it came from, and nothing has to keep the
  source alive for it.
- The string view, `str` (`typeinfo.TypeString` tag 1). A slice IS one, as the
  checker types it, of an owned string or of another view; a `str` parameter
  is a borrowed reference like any other; a `str` binding holds one. Every
  read — the length, a byte, a window, `==` / `!=` and `+` — is blind to which
  string type holds the bytes (`ssasem.is_text`), and `+`'s result is owned.
  Physically a view is a box over the source's bytes carrying the immortal rc
  sentinel, so `ssarc` releases a view-typed unit through
  `__fern_str_view_free`, which frees the box alone on the sentinel and takes
  the ordinary path otherwise (wasm slices copy). The release helper is now
  chosen by the TYPE, not by finding the slice instruction behind the value.

  Crossing between the two string types is the `str_as` kind, an identity
  that borrows its operand the way `variant_up` does: an owned string reaches
  a view-typed destination — a `str` binding, a `str` parameter — as a borrow
  of its box, and a view reaches a BORROWED `string` parameter (a method's
  receiver included, which is how `to_owned` and every other std/string
  method takes one) the way the checker's `str_arg_borrow` carve-out lets it.
  A counted `string` parameter is not offered the retag: it would take the
  box as its own, which a view's box is not. A string's methods are declared
  in the standard library on a `string` receiver, so a text receiver now
  resolves `string.<m>` contracts the way a struct's does.

  What a view may NOT do is escape its source. A function whose result is
  `str` is refused ("view result escapes its source"): the caller's model has
  no anchor for a result to its argument. A view as an array element — an
  array literal's, or `.append`'s — is refused too ("view element escapes its
  source"): the array may outlive the source. The checker's borrowed-argument
  carve-out lets `xs.append(slice_unchecked(s, a, b))` through today — the
  escaping position docs/STR-VIEW-CONTRACT.md's decision hands to #8635 —
  and this boundary refuses it rather than inheriting the hole. The
  compiler's own sources did it in 26 places, split helpers handing back a
  `string[]` of windows onto their argument, each element a leaked view box
  on the register backends; they copy now (`+ ""`), which is what the
  checker rule will demand of them.

- A function VALUE and the call through one. A bare name at a function-typed
  destination is a declaration's ADDRESS (the stack IR's `const_func`), typed
  by the contract this boundary derived for that declaration — so the value
  joins the call table and a refused declaration refuses its referrer exactly
  as a call does. Calling a bound name that holds one is `call_indirect`, and
  its whole contract is the function TYPE, since no declaration is in hand: a
  function type spells no `own`, so every reference argument is LENT — the
  convention irlower's own env-box trampoline states, and enforces by
  un-`own`ing a parameter it trampolines — and the result is a unit of the
  caller's own the way a direct call's is.

  The address of a declaration with a COUNTED parameter is refused ("function
  value consumes an argument"): it promises the opposite of that convention,
  and a function type has no way to say so. The AST path takes the shape and
  leaks it — `apply(eat, a)` for `eat(own xs: i32[])` frees nothing — which is
  a checker hole this boundary declines to inherit rather than a rule it
  invents.

  The address is one i32-shaped word, so it is a value and a parameter and
  never an element or a declared field: a container slot holds a counted
  reference, and an AST-lowered read of a fn-typed field takes an env box
  rather than the bare address. A function-typed RESULT is refused for the same
  reason. Every slot of the signature is that same word — a wide parameter or
  result is refused ("function signature slot"), because the untagged
  `call_indirect` this boundary emits describes each slot as one, and a
  funcref type is structural on wasm.

Refused, each with its own reason: calls of the remaining builtins, a void
call in expression position, the 32-bit float, the unsigned and
pointer integer widths, string ordering, generic records, the struct,
nested and `@`-bound destructuring forms, labelled loops, match guards and
the pattern shapes above,
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
  tuple and array results under `ssarc.caller_sigs`, the byte and cast shapes
  below, the enum shapes above, every receiver the planner does not move
  (a borrowed parameter's box, a record field's, an element of a borrowed
  array of arrays, an `own` parameter's read again after the push, the field
  receiver appended to in a loop, and a `string[]` through both builtins, each
  asserting the source keeps its length and its elements), the loop header
  whose first step pushes on the caller's own box, and the view shapes: a scanning loop binding
  a slice per iteration and comparing, measuring, indexing and lending it to
  a `str` and to a borrowed `string` parameter; an owned string lent to `str`
  bindings and re-lent; a view of a view; a view whose source is a temporary
  whose only use is the slice; and a view receiver on a `string` method. The
  float shapes: a quotient sign-flipped and scaled, the three comparison
  answers, a loop-carried accumulator, an f64 crossing a produced-to-produced
  call as a parameter and a result, and an i64 converted to f64 and back. The
  function-value shapes: an address handed to a produced callee and called
  there, a scalar and a reference argument lent across an indirect call, a
  temporary array literal lent to one, an indirect call whose counted result
  the caller owns and one whose result it discards, and an address carried
  through a branch join. The wasm leg is what holds an indirect call to its
  ABI: a lost slot class there is an `indirect call type mismatch` rather than
  a wrong number.

The byte and cast fixtures pin what only an execution can show: a byte
arithmetic result that wraps at eight bits, an i32 one that wraps at
thirty-two, a narrowing cast and a widening one, and a byte read out of a
string, compared, replaced and widened. Both width masks are pinned through a
COMPARISON rather than a printed value, because a printed result is truncated
to 32 bits on its way out and reads the same whether the register held the
wrapped value or the wide one.

The wide fixtures answer with numbers that need more than 32 bits to reach:
`1i64 << 40` read at the low word is 0 where a masked 32-bit count would give
256, and a product past 2^32 read through `>> 32` is 1 where a wrapped one is
0. One pair crosses a produced-to-produced call as a result and then as a
parameter, which is where a lost slot class is a wasm validation error rather
than a wrong number.

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

The parameter rows are rewritten the same way. The AST readers take them
from the callee's syntax, where a borrowed parameter handed on to a counted
one reads as consumed, and an argument the rows call neither borrowed nor
counted is taken to be the callee's, so a temporary the caller built for it
is never released — `copy_set(xs: i32[])` passing `xs` to an `own`
parameter leaked the caller's literal. The contract says what a parameter
is: a borrowed reference parameter is only ever read or retained, which is
the counted-tier row (`ACNT:` / `SCNT:` / `PCNT:` / `ECNT:` / `TCNT:` /
`DCNT:` by the parameter's type, and the merged `CNT:` row) — every
reference the callee keeps was retained, so exactly one release stays with
the caller — and never the bare borrowable row, which promises the callee
keeps no reference at all. A counted parameter takes the one count the
caller hands over, which is the row-less reading already.

## Remaining

The producer does not yet admit the unsigned and pointer integer widths, the
32-bit float, the remaining builtins, the struct and nested destructuring
forms, closures or generics, so no production consumer is switched and no
AST ownership analysis is deleted.

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
results, still rely on the AST caller's own syntactic rows. So does a borrowed
record PARAMETER, and that one is measured: when a produced callee RETAINS a
counted-element array field of one, the AST-lowered caller's release of the
record frees its box and leaves the field's buffer — 40 bytes for a
two-element `string[]`, 56 for a four-element one. Retaining the field into a
tuple is enough; no array builtin is involved, and the same program lowered
entirely by the AST pipeline balances.

Constructions over a loop element cross too (`bump_each`, `line_each`,
`word_recs`): a functional update and a variant literal built from the
element, an unannotated binding inferred from it, and a string element
retained into a record whose own unit dies at the end of the step.

Measured against the whole loaded self-hosted compiler, 6,299 of its 7,738
functions produce, plan and physically lower.

`examples/self_host/semsource_census_run.fern` is the instrument: it loads a
module tree the way the production compiler does and counts the stage each
function reaches, tallying refusals by LEAF — the first refusal in a chain
that is not a `call target was refused` cascade. That distinction is the whole
point of the report. The raw histogram is dominated by functions whose only
problem is a refused callee, so it names work that is already done.

The leaf is also what to probe against before building, because measured
deltas have repeatedly disagreed with even the leaf histogram. String
indexing was the largest leaf and worth +0 until enough of its callers
lowered; the byte type below was worth +1,200 because it unblocked three
leaves at once.

The leaves are now led by callees with no semantic contract (1,016), a variant
field whose type is unresolved (98), a binding whose type is not its value's
(75) and record literals (74). The string view was worth +355 once the sites
that stored one were made to copy, and the f64 +273 — each measured, against a
leaf histogram that had ranked them differently. The declared field width was
worth the 81 it was measured at — every construction with a wide field,
`ir.Op`'s f64 and i64 among them, resolved its declaration — and `.with` +233:
the builtin-call leaf was 453 functions of which 439 were a `.with`, which a
probe keyed by callee showed and the bare leaf histogram could not. The callee
leaf is the same shape: keyed by name it is a handful of runtime builtins
(`strbuf_append`, `__memchr`, `eprint`, `env`, `read_file`) and the `astwalk`
folds that take a function value, each with its closures behind it. The unit
planner refuses nothing now; what produces and plans but does not lower is 14
functions with a value type physical RC does not carry. The flat tuple
destructure was worth +19 lowered against a leaf of 321: a probe keyed by
pattern shape showed the leaf was entirely the flat tuple form, and nearly
every function holding one refuses again one leaf further in, which is what
moved the callee, name and record-literal leaves up. The module-level constant
closed the name leaf outright, +78 lowered of 393: a `const` is a
zero-parameter function to the parser, the AST lowering calls one on a bare
reference, and so does this boundary, when the name has a zero-parameter
contract whose result is the checked type — a reference to a function VALUE is
typed as a function, never as the result, so it takes the address form above
instead. Nearly every constant's reader refuses again at the callee leaf. The
void call and the six builtin contracts were worth +201 together; keyed by
callee, the leaf that remains is the function value — the `astwalk` folds and
the closures behind them — and the builtins whose result is an enum (`env`,
`read_file`) or whose vocabulary is not here yet. The record-literal leaf is
the same leaf in disguise: a probe keyed by the checker's reason showed every
one a field whose value is such a call. The literal tree, the
destination-typed literal and the string loop were worth +62 together: the
iterable leaf (67) closed outright and the two literal mismatches with it.
What remains of the binding-mismatch leaf is the lifted lambda body reading
its captures out of the untyped `__env` word array — a closure shape, not a
literal one. A shift whose count is another integer width — `n << k` with `n:
i64` and `k: i32` — was the operator leaf (52); the count now reaches the
operator through a `cast` to the value's width, which is the masking the
runtime does anyway, worth +41.

The no-contract refusal names its callee now, so the census splits that
leaf by builtin on its own: the `astwalk` folds that take a function value
(some 530 functions between them), `env` (145), `read_file` (26), and a
tail of runtime builtins. Five of the tail took contracts for +78 —
`write` and `exit` as void calls, `string_from_bytes_unchecked` handing
back a fresh string, `f64_bits` and `f64_from_bits` as values — each
lowered to the stack IR op the AST lowering already emits for it.

The loop element's checker binding closed the record-literal leaf from 226 to
74, +93 lowered. A probe naming the checker's reason at every refused literal
showed its 41 direct sites were all a field value the checker could not type,
and all but three of those an expression naming a `for` element the scope had
never bound. The same binding closed the unresolved-binding-type leaf outright
and carried every other construct whose type the checker settles inside a loop
body with it — array, tuple and variant literals, an unannotated binding, an
operator's width. What remains of the record-literal leaf is three sites and
no record shape: two are a field whose value calls a runtime builtin the
self-host CHECKER has no signature for (`string_from_bytes_unchecked`,
`f64_bits`), so `check_expr` hands back the unknown that collapses the
literal, and one is a field whose value is a call taking function values. A
builtin signature table for `check_expr` would move the first two, but it
retypes every builtin call in the twelve diagnostic walkers with it, so it is
its own change rather than a record one.

The `.append` receiver gate is gone, +295 planned and +288 lowered. A probe
keyed by the receiver's mode and defining instruction ranked the 295 refusals
and disagreed with the note this gate was deferred on: not one receiver was
owned-but-live, and not one was a borrowed-parameter accumulator in a loop.
They were 151 appends and 5 withs on a borrowed PARAMETER, 99 appends and 39
withs on a record FIELD read — the immutable-update threading shape, where the
AST lowering already clones (`irlower.lower_arr_append_value`) — and one on an
array element. The loop accumulator plans either way,
because what its body appends to is the header PHI, and a phi is a unit of
this function's own however its sources reached it.

What the gate was guarding was real but was not the receiver's mode: the
runtime's push gives the receiver's unit back only at rc == 1, so a receiver
shared at the push kept a count nobody released, and the borrowed-parameter
accumulator that planned leaked one buffer per call. Emitting the count test
in this lowering rather than leaning on the shared helper closed that and made
the mode irrelevant in the same move.

The copy the not-moved receiver takes is one whole array per push, which is
O(n^2) bytes when the shape is a loop: a field-receiver accumulator over n
steps allocates 3n + 2 blocks and copies 4n(n-1) bytes, measured at 770
allocations for n = 256 against 8 for the sole-owner local form, and 2.3 s
against 0.001 s at n = 16,384. That is the cost the AST lowering pays too
until its escape analysis (`irlower.field_append_inplace_sites_of`, native's
`fieldPlaceAppendCopies` inverted) exempts a site; the analogue here is the
next optimisation this boundary wants, not a correctness gap.

Next, by measured leaf: the callee leaf is two things, a contract for the
runtime builtins a body calls, which is a vocabulary question and not a leaf,
and the function value, whose shape half the indirect call form below closed.
That was worth +3 lowered, which is the measurement and not the histogram:
the fn-value leaf is almost entirely the `astwalk` folds, and those are
refused for a reason a form for the ADDRESS does not touch.

**The `astwalk` fold family stays refused, and the reason is not vocabulary.**
`docs/ERASED-GENERICS-RC.md` states the three candidate unit rules for an
erased accumulator and what each costs; the summary is here. It is the largest
single leaf — 547 functions between `fold_stmt_nodes` (224), `fold_stmt_spine`
(142), `fold_expr_pruned` (119), `fold_stmt` (40), `fold_stmt_pruned` (12),
`fold_expr_nodes` (7) and two more — and every one of them threads an
accumulator typed by an ERASED type variable, for which this boundary has no
sound unit rule:

- The per-module emit path runs no monomorphiser, so one body serves every
  instantiation and can emit no retain and no release on the erased word:
  `__fern_rc_dec` on a `T` bound to i32 would decrement an integer.
- A fold REPLACES that accumulator once per visited node
  (`acc = visit(st, acc)`). Under the function-value convention the visitor
  lends its accumulator and hands back a unit of the caller's own, so every
  step creates one erased unit and abandons the previous one. Only the fold
  sees those intermediates, and the fold is the one body that cannot release
  them. Calling the erased result no unit instead leaks it at the caller;
  calling it a unit makes the fold release a word it does not own at every
  scalar instantiation.

So an erased word is soundly expressible only where it is PASSED THROUGH and
never replaced, which no fold is, and a contract with an erased parameter
needs a call-site instantiation this boundary also does not have. Two further
facts sit underneath. The visitors the compiler
actually passes do not agree with each other — a lifted `(s, a) => a` hands
back the argument it was lent, where `fwd_scan_stmt` hands back a fresh box —
so there is no one convention to write down even for the concrete callees.
And the checker erases `T` for only half the family: only a PROMOTED
generic's variables land in `FuncDecl.type_params`, and
`type_from_name_erasing_tparams` has nothing to erase against without them,
so `fold_expr`, `fold_stmt`, `fold_expr_pruned`, `fold_stmt_pruned` and
`fold_stmt_own` reach this boundary with an unresolved `T` where
`fold_expr_nodes`, `fold_stmt_nodes`, `fold_stmt_spine` and
`fold_stmt_own_pruned` reach it erased. Admitting the family needs the
function-value ABI settled first — whether an indirect callee may consume a
reference, and what a generic's erased result owns — which is a language
decision, not a vocabulary one.

Next, by measured leaf: the unresolved variant field type (98), the binding
whose declared type is not its value's (75), the record literal (74) and the
builtins whose result is an instantiated builtin union, `env` (158) and
`read_file` (26); all shapes rather than vocabulary. Then a production
consumer that lowers produced functions through this pipeline and feeds
`caller_sigs` to the remaining AST callers.
