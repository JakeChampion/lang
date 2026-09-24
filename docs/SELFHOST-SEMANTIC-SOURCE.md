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
  concatenation, and every string comparison, typed by `ssasem.binary_result`
  beside the scalar rules. Equality is the runtime's `str_eq`; an ordering is
  its `str_cmp` — a signed i32 under, at or over zero — against a zero
  constant, the same shape the AST lowering emits. A string constant and a
  concatenation are units of the function's own, released when dead; a
  literal's static box is immortal to the runtime, so its release is a no-op.
  `for c in s` over a string is one `str_index` read per step, the byte typed
  u8 as the checker types it, with the text borrowed for the loop's span.
- The integer operators — arithmetic, division and remainder, the bitwise
  three, and both shifts — plus integer comparisons, boolean `==` / `!=`, unary
  `-` and `!`. These are new semantic kinds (the SSA `binary` / `unary` tags)
  whose typing lives in `ssasem.binary_result` / `unary_result`; `ssarc`
  lowers them to the stack IR's `add` … `ne` and `not` / zero-minus. Every one
  is total, because Fern pins the integer edges rather than trapping
  (`docs/INTEGER-SEMANTICS.md`): `x / 0` is 0, `x % 0` is x, `INT_MIN / -1`
  wraps, and a shift count is masked to the operand width. So none of them
  needs a guard, a branch or an abort path here.

  Each runs at ONE width AND signedness: both operands carry the same integer
  type and so does the result. Fern has no implicit numeric CONVERSION, so a
  byte and an i32 cannot meet at all. The checker does auto-widen the narrower
  side of a same-signedness pair (`i64 + i32`), rewriting the operand with a
  cast, but
  the tree this producer reads is not the rewritten one — so a mixed-width
  operator is refused here rather than promoted on a guess.

- The integer types are i32, u8, u32, i64 and u64. A byte and a u32 ride the
  same i32-shaped slot on every backend, so they cost no new physical
  representation — what distinguishes each is the range, and `+`, `-`, `*` and
  `<<` mask their result back to it. **So does an i32's**: the stack IR runs
  the operators in a register wider than either type, and without the mask a
  produced
  `2147483647 + 1` was 2147483648 rather than INT_MIN. The AST lowering has
  emitted that step since #3581; this boundary did not, and the executable
  fixture now pins both widths through a comparison (a printed result is
  truncated on its way out and reads the same either way).

- An i64 or u64 gets a slot of its own, which only wasm spells out: in the
  function's type for a parameter and a result (`irlower.result_i64`), and in
  its locals otherwise. Its operators run at width 64 and are already
  full-width, so nothing masks after them; negation pushes its zero at that
  width too, or the subtraction's two sides disagree. A 64-bit literal does not
  fit the semantic constant's i32 immediate at all, so it carries the literal's
  SOURCE TEXT instead — the form the backends splice straight into a 64-bit
  immediate, and the one the AST lowering already uses for the same literal. A
  u32 literal has the same problem one value short of half its range, so the
  u32 takes the text form at every value rather than at some of them
  (`ssasem.text_constant`). An integer literal TREE — `0 - 1`, `1 << 40` — is
  the width of its destination, or of the operand beside it, before either
  side is produced, the way the checker settles it; without that a 32-bit
  `0 - 1` fails the exact-type rule at an i64 binding.

  It is a VALUE here, a record or variant FIELD and an array ELEMENT, never a
  TUPLE element. An array's element ops each carry their own width, so a wide
  element rides the eight-byte stride wasm needs (`op_arr_make_i64` and its
  get/set/push siblings, which name the SLOT rather than the sign, so a u64
  takes the ones an i64 does). `op_tuple_make` spells no element kinds, so a
  wide tuple element has no store width to be written at and a tuple TYPE
  carrying one is refused. A record or variant stores each field at the width
  its declaration names: the construction carries its declaration's index
  (`op_struct_make`), a record's resolved by name and a variant's by name
  within its enum, since two enums may declare one variant name with
  differently sized payloads; the physical lowering takes the declaration
  table for it, as the AST lowering does. A wide operand of a construction
  whose declaration is not in the table is refused rather than built through
  a narrow store; a read takes its offset from the field index alone and its
  width from the schema this boundary verified the projection against.

  An unsigned operand takes the operator forms that read no sign bit — the
  four orderings, the right shift, division and remainder — through
  `irlower.to_unsigned_kind`, the same remap the AST lowering applies. The
  byte goes through it too, though nothing turns on that: 0..255 is inside the
  signed range at every slot width. Negation is refused at every unsigned
  width, as it is at the byte: its result leaves the range the type names, and
  the wrap this boundary would emit is not what the source means by it.

- `usize`, the pointer-width unsigned integer: an address, 32 bits on wasm
  and the whole 64-bit slot on the register backends, which the checker now
  resolves (`t_usize`, native's `WidthPtr`) where it resolved the spelling to
  unknown. It rides the narrow slot, which is the address's own width on every
  backend, and converts to and from the 64-bit integers and nothing else: the
  conversion is the stack IR's extend or wrap carrying `width_ptr()`, which
  wasm emits as the i32 forms and the register emitters as nothing, since an
  address already fills the slot (the i32 forms there mask the low word, which
  is what truncates a pointer). An operator at it is refused: it would run at
  the i32's width on a register backend, which is not the address's; so is a
  literal at it. The string builder's handle is one, and its seven builtins
  have contracts: the handle is a value with no unit and no drop, the pushes
  read the string they copy, and `buf_take` hands back a fresh string of the
  caller's own (`handle_out`, `handle_in`, `built`; the RC leg runs the
  builder and round-trips a live handle through an i64 on every target).

- A cast between the integer types, or between one of the four 32- and 64-bit
  ones and the f64, as the semantic `cast` kind. `e as T` reaches the producer
  as a unary whose operator names T, and the destination is the checker's type
  for the whole expression rather than the spelling. Crossing the 64-bit
  boundary is an explicit extend or wrap, and the SOURCE's signedness is what
  an extension extends by: only the signed 32-bit width fills the high half
  from a sign bit. Inside the i32 domain the conversion is the destination's
  own mask — the byte's 255, the u32's zero-extension of the low word, the
  i32's sign-extension of it — and nothing at all where the operand's type
  already keeps its value inside that range. Into the f64 is the convert at the
  operand's width and signedness, and out of it the truncation toward zero at
  the result's, unsigned where the destination has no sign bit — real
  instructions, which is why the byte, whose convert has no opcode at that
  width, is not offered them and reaches the float through the i32 the source
  writes. A cast to or from anything else — a string, the `as?` downcast — is
  refused.

- The f64, as a VALUE, a declared field and an array element but never a tuple
  element, for the same reason the i64 is one (`narrow_slot`): it gets a slot
  of its own that only wasm spells
  out (`irlower.result_f64`, the `f64_slots` a produced body declares). A
  literal is an f64 — the checker types it polymorphic and settles it where it
  lands, so a `f32` suffix or an f32 destination makes it an f32, and a
  binding with no annotation settles it at the f64 it is — and it
  carries its source text the way a wide integer does, for the backends to
  splice. The four arithmetic operators are the stack IR's own float opcodes
  and never wrap, a comparison is a boolean, negation is the sign flip; `%`
  has no float opcode and the bitwise operators no float meaning, so both
  refuse. An operator decides its width the way the integer ones do — from
  the checker's type for the whole expression, or from whichever operand
  bears one — so a literal on either side takes it.

- The f32, riding the f64's slot at single precision: every value of the type
  is an f64 the `f32_bits` / `f32_from_bits` round-trip has already rounded,
  so the width is a rounding applied wherever a value of the type is made —
  a literal, a conversion into it, an operator's result at it — rather than
  a slot of its own, and its operators are the f64's with the rounding after
  them. A conversion between the two widths is admitted, as one between an
  integer and either width is; the bit pair reads and writes the rounded
  value (`narrow_bits`, `widened`, `narrow_sum`, `narrow_float`; the RC leg
  pins the odd integer past 2^24 rounding back, a literal's, a sum's and a
  declared field's bit patterns, and a comparison).

- Integer literals in both bases the lexer writes, decimal and hexadecimal.
  A suffix names the type outright, which is how a byte literal (`b'x'`)
  carries its width; an unsuffixed literal has none of its own and takes the
  integer type of what it is written against, so `var b: u8 = 65` binds a byte
  and `65` in an i32 position an i32. For an operator, that width is decided
  BEFORE either operand is produced — from the checker's type for the whole
  expression, or from whichever operand bears one — so a literal takes it on
  either side. A literal that rides the instruction's immediate takes
  `util.lit_to_i32`'s value, the same one the production lowering reads, and a
  constant outside its type's range is refused rather than wrapped; one at a
  width with no immediate to ride (`ssasem.text_constant` — the 64-bit types
  and the u32) carries its source text instead, which the backends splice.
- `&&` and `||` as control flow: the right operand runs on its own edge and the
  result is a boolean phi.
- `if` / `else` with binding joins, `while` and `loop` with labelled or unlabelled `break`
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
  so an arm after a wildcard is unreachable and is not produced. A pattern
  may be spelled with the union's own name ahead of the variant
  (`Shape.Dot`). A nested, tuple, struct-field, literal or `@` pattern, and a
  non-enum scrutinee are refused.
- Paths headed by a TYPE name. `E.A(7)` and `E.B` construct the variant of the
  enum they name, `Option.Some(k)` the builtin's; `Point.make(3, 4)` calls the
  associated function an impl declared on the struct, which the contract
  table keys the way it keys a method (`Point.make`) and whose parameters
  are the declared ones, with no receiver. A bound name shadows the type.

  A match whose unguarded arms NAME distinct variants, as many as the union
  declares, is TOTAL: a value is one of them, so the last arm is entered
  unconditionally and carries no test of its own, and nothing falls through
  to the join. Coverage is read off the declarations here rather than taken
  on the checker's word, though the checker proves the same thing (E030) —
  from the enum declaration, from an INJECTED enum's variant structs, or
  from a builtin union's two positions, whichever names the scrutinee's
  variants. A value-returning body whose last statement is a total match
  therefore needs no `return` after it, which is what the AST lowering and
  every other Fern backend already assume.

  A GUARDED arm is read after its payload bindings, in the arm's own block:
  a true guard enters the body and a false one leaves for the next arm's
  test, with the bindings released on that edge as on any other. A guarded
  arm never counts towards totality, and a guarded wildcard does not close
  the chain.

  The parser desugars a nested, tuple, struct-field or literal arm pattern
  into a done-flag chain of flat matches (`build_nested_arm_match` and its
  siblings) that falls through by construction, and the checker's E052
  reads every arm of the chain returning as a body that cannot reach its
  end. That live end is the `unreachable` terminator here: it releases what
  the frame still holds, as a return does, and aborts. It is the one
  terminator the plan and the verifier admit with no value, and it is only
  sound because the module was checked.

  `ssasem.analyze` carries the matching rule, because the closing arm
  projects its payload with no test above it. A variant projection is
  guarded when a dominating test HELD for that variant — the sole-true-edge
  rule — or when every OTHER variant of the enum was REFUTED on the way in,
  each by a dominating test's sole false edge. A value never changes
  variant, so either settles it for every later use.

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
- The builtins whose RESULT is one of the front end's own generic unions:
  `env` and `read_line` hand back an `Option[string]`, `read_file` a
  `Result[string, IoError]`, `read_file_bytes` a `Result[u8[], IoError]`,
  `read_dir` a `Result[string[], IoError]`, and `stat` / `lstat` a
  `Result[FileStat, IoError]`.
  Each contract names the INSTANTIATION, because that is what says what the
  caller owns — the box owns its payload, and the payload owns whatever it
  owns in turn. Each has exactly ONE instantiation, fixed by the builtin
  rather than by a call site, so none is a template.
  A builtin whose result is `Result[void, IoError]` — `write_file`,
  `create_dir_all`, `rename`, `chmod` and the rest of the outcome ops — names
  it the same way (`outcome_contracts`, `fs_op_contracts`), the unit payload
  being a box with nothing in its slot.
  The OS floor the compiler itself never calls has contracts too
  (`os_contracts`, `handle_metadata_contracts`): the process and host
  queries, the directory, link and permission ops, `temp_dir`, `statfs`,
  `subprocess` with its `ProcessResult` record, the sockets' connect, the
  signal and process ops, the terminal's `termios_get`, `termios_set` and
  `set_window_size`, and the handle metadata asked of the bare
  descriptor like `close` — `stat`, `flags`, `seek`, the three syncs,
  `write_some`, `truncate`. Every string and array argument is lent, a
  scalar is a value, a fresh string or array is the caller's. `ssarc`
  emits each as the stack IR op the AST lowering emits
  (`os_query_site`, `fs_op_site`, `handle_metadata_site`);
  `docs/rc-log/2026-09-20-the-os-floor-has-contracts.md` has the census
  they moved.
  A declared generic enum is still refused. `map_new` and `cell_new` are the
  builtins that cannot join them: the destination type drives `Map`'s and
  `Cell`'s arguments, so one contract could not name the instantiation.

  The box is a tag word and one payload word (`semrecords.layout_option`),
  not the shape pointer a declared enum's variant box carries, so its
  variants come from the type arguments rather than from a declaration: Some
  and Ok at position 0, None and Err at position 1, the position being the
  tag the box stores. `variant_new` is `opt_make` / `opt_none`, `variant_is`
  the tag compared against that position, `variant_get` the payload read
  without a field index, and the release walks the payload the tag selects
  before `__fern_rc_dec` frees the box. The one payload word is i32-shaped,
  so an i64 or f64 payload is refused ("wide builtin union payload") for the
  reason an array or tuple element is.

  A literal of one — `Some(x)`, `None`, `Ok(x)`, `Err(e)` — takes its type
  arguments from the DESTINATION, since the checker types a builtin variant
  by its enum alone and never by an instantiation.

  An enum the front end INJECTS rather than the source writing it
  (`IoError`, `JsonValue`) has variant declarations and no enum declaration,
  so its variant list is read off the struct table's enum owner
  (`checker.owned_variant_names`). That is what gives a failed read's Err
  arm a schema.
- The builtins whose result owns nothing: `args` (a fresh `string[]` of the
  caller's own), `putchar`, `f32_from_bits` and `__rc_underflow_count`.
- The host itself: the three clocks (`monotonic_ns`, `now_ns`, `now_unix_ms`),
  the kernel's randomness (`random_bytes`, `random_i32`) and the two sleeps
  (`sleep_ms`, `sleep_ns`). None is handed a reference, so none borrows; the
  clocks and `random_i32` own nothing, `random_bytes` hands back a fresh buffer
  this frame owns, and the sleeps answer nothing. A contract is half the work
  for any of these: each is also an op of its own in the physical lowering, the
  same one the AST lowering emits, because a name with a contract and no op
  reaches the backends as a direct call to a symbol no runtime defines.
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
- The ARRAY slice, `xs[lo:hi]` on an array of scalars, which the checker
  types `[T]` and the runtime copies into a fresh array (`arr_slice`, 4- or
  8-byte elements by the element width). `ssasem.arr_slice` produces it as an
  owned value of the source's type with the bounds left to the runtime, as
  the AST lowering leaves them; an open end reads the source's length. An
  array whose elements own something is refused: the runtime copies the words
  without a retain, so the copy would alias them.
- The CHECKED slice, `s[a:b]`, which the checker types `Option[str]`. The
  window is admitted when `0 <= a <= b <= len` and neither end lands inside a
  codepoint, and the expression answers `None` when it is not. Every test that
  fails branches to one `None` block, so the whole check is a decision tree
  with two sinks, and the result joins as a single phi of the `Some` of the
  view and that `None`. The length is read once and each bound evaluated once,
  ahead of every test, so a bound with a side effect runs exactly as often as
  it is written. The boundary test is interior-only, as the AST lowering's is:
  an end at 0 or at the length is a boundary by construction, and anywhere
  else the byte there must not be a continuation byte. An `Option` of a
  reference is a nullable pointer, so the `Some` declares no unit of its own
  and the slice's unit is what the frame releases — the payload IS the view.
  Slicing an ARRAY is refused: it answers a bare view rather than an `Option`,
  and the second reference to the source's buffer that it hands back is a kind
  this vocabulary does not have.
- The try operator, `e?`, which is control flow rather than an operator and so
  never reaches `ssasem.unary_result`. The operand's tag is tested against the
  success variant; the success edge unwraps the payload and the expression
  continues with it, and the failure edge rebuilds the failure at this body's
  own result type and RETURNS. Nothing joins, so there is no phi: the value is
  the success payload and the block left open is the success edge. Success is
  the first variant declared and failure the second (`docs/TRY.md`), and native
  has enforced both that shape and the `@try` opt-in (E078) before this runs. A
  failure carrying a payload moves it out of the operand with a `variant_get`
  and into the `variant_new` that builds the outgoing one; a failure carrying
  more than one, or one whose payload type differs from the body's own failure
  variant, is refused.
- Indexing a string, as one byte handed back in a u8 — the type the checker
  gives the expression, so a binding or an operator over it needs no
  reconciliation. The receiver is read the way a length's is and the result
  owns nothing, so — unlike the slice beside it — this is NOT a projection:
  the byte outlives the string it came from, and nothing has to keep the
  source alive for it.
- The string view, `str` (`typeinfo.TypeString` tag 1). A slice IS one, as the
  checker types it, of an owned string or of another view; a `str` parameter
  is a borrowed reference like any other; a `str` binding holds one. Every
  read — the length, a byte, a window, a comparison and `+` — is blind to which
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
  method takes one) the way the checker's `view_arg_borrow` carve-out lets it.
  A counted `string` parameter is not offered the retag: it would take the
  box as its own, which a view's box is not. A string's methods are declared
  in the standard library on a `string` receiver, so a text receiver now
  resolves `string.<m>` contracts the way a struct's does.

  What a view may NOT do is outlive its source. A value that holds views —
  a checked slice's `Option[str]`, a variant or a phi carrying one — is
  anchored to the one value holding the bytes they read (`ssasem.bytes_root`),
  so that source stays alive until the last read; a value gathering views of
  two sources has no single anchor and is refused. A function whose result
  holds a view is anchored to the one parameter those views read
  (`semsource.anchor_module`, `ssasem.Anchor`), and its caller anchors the
  call's result to that argument, which is what keeps a temporary receiver
  alive past the call. A result reading a local, or either of two
  parameters, is refused ("view result escapes its source"). The parameter
  stays lent: a returned view reads it without taking its unit. A view as an array element — an
  array literal's, or `.append`'s — is refused too ("view element escapes its
  source"): the array may outlive the source. A map INSERT's key is the same
  position — it joins the key column, which releases it when the map is
  released — and is refused for the same reason. A map READ's key is not:
  `get_or`, `has` and `delete` hash and compare it and `operation_supplies`
  counts no unit for it, so a view there is an ordinary lend and takes the
  retag a borrowed `string` parameter offers. Both checkers refuse the
  stored positions too (E038 at `append`, `with` and `insert`, #8635), so
  a checked program no longer reaches these refusals; they stay as the
  boundary's own statement of the rule.

- A function VALUE and the call through one. A value is the environment BOX
  the lambda lift builds before this boundary reads the tree: one allocation
  holding the hoisted body's address in slot 0 and the captures after it.
  `__mkclo$<body>(caps…)` is the constructor (the semantic `closure_new`),
  typed by the contract this boundary derived for that body MINUS the `__env`
  parameter the body reads its captures through — so the body joins the call
  table and a refused one refuses its constructor exactly as a refused callee
  refuses its caller, and the address it names is a symbol the module defines.
  The box is a fresh unit of the constructing function's own, and it owns one
  unit of every capture that is a reference.

  The captures are named by the body's ENVIRONMENT RECORD. The lift hoists a
  capturing body with `__env: i32[]` first and reads each capture at the top
  of the body as `var cap: T = __env[1 + i]`, one per slot in slot order, so
  those leading reads say what the box holds: the producer types the `__env`
  parameter as `__env$<body>` with the capture types as its arguments
  (`semtypes.is_env`), a record whose fields are the address word and then
  one per capture. A record's fields ride the slots an array's elements do —
  a shape word where the array keeps its length, then one word per field —
  so `__env[k]` is the record projection of field `k` typed by the capture,
  the constructor is checked against the same list (count and type, one lift
  writes both sides), and the box's release walks the captures with the
  record's own drop helper. A capture that is itself a FUNCTION value is no
  exception: the box takes a unit of it and its release walks the field, like
  every other reference it holds. It used to BORROW one instead, secured by
  requiring the capture to be a borrowed PARAMETER whose owner outlived every
  box built here — a rule that could not describe a box that ESCAPES, since a
  local closure captured into a returned box is owned by a frame that is going
  away and the box never took it, so nobody freed it (#9637). A function type
  names no capture, so a box is
  matched at run time: the drop compares slot 0 with the address of each
  environment paired with that function type and calls that helper, and a
  box that matches none — one whose captures own nothing, or one built by an
  AST-lowered caller, which lends its captures — is released alone. A frame
  that only RECEIVES a function value never built one, so its graph names no
  environment of its own; `semsource.env_rows` pairs every environment a value
  of that type could carry with the type itself, read off the module's whole
  contract table, which is what lets a returned closure be released by the
  frame it was handed to. The pairing is the TYPE's rather than the frame's,
  which it has to be: a drop helper is emitted by every function that mentions
  the record, and two copies that disagree are a refusal; the rows close over
  the schema table (`env_rows_closed`), so a frame reaching the type only
  through a field lists the same environments as the frame that built it. A wide
  capture is refused at the physical layer for the reason a wide TUPLE
  element is — the env box stores each slot through `op_arr_make` at width
  32, the one array construction here that is not written at its element's
  own width — and the lift declines one before that.

  Calling a bound name that holds one is `call_indirect`, environment-FIRST:
  the box, the written arguments, then the address out of slot 0. That is the
  one ABI a function type has here, because it is the one
  `irlower.lower_func` gives every fn-typed parameter of a free function and
  The whole contract is the
  function TYPE, since no declaration is in hand: it carries a per-parameter
  CONSUMING mask, and every reference argument at a slot the mask leaves unset
  is LENT. The result is a unit of the caller's own the way a direct call's is.
  The box itself is lent too, never consumed.

  A CONSUMING slot is one the type spells `own`: the caller hands its unit
  over and takes back the one the call returns, and the indirect call
  supplies a move rather than a read.

  `ssasem.closure_type` builds the mask a box hands out from the BODY's
  declared modes — a counted parameter is a consuming slot — so the type every
  call through the box is checked against and the body inside it cannot
  disagree, and the trampoline the lift builds around a bare name mirrors its
  target's modes for the same reason. The environment parameter is the box
  itself, lent like a receiver and absent from the type the box hands out, so a
  counted one there is refused ("function value consumes its environment") as a
  promise nothing can state. `own` on a SCALAR is normalised out of the type:
  the slot carries no unit, so nothing changes hands there, and that is what
  lets one generic body serve a scalar and a reference instantiation at once.

  A BARE name at a function-typed destination is a declaration's address,
  which is not what a value holds here, and is refused. The lift wraps one
  into a `$wrapN` trampoline's box before this boundary sees it, so the
  refusal is unreachable in a module any backend lowers: removing the address
  form moved the census by nothing, measured on its own.

  The box is one i32-shaped word: a value, a parameter, a record field, a
  variant field, an array or tuple element. Releasing the holder walks its
  slots by their declared types, and a function type names no captures of its
  own, so the walk matches the box's body address against the environments
  the schema table names (`semsource.env_rows`, `ssarc.drop_captures`) and
  releases the captures it finds. What stays refused is a function value
  NESTED in a field — an array or tuple of them behind a record or variant
  field — and a function-typed RESULT.
  A parameter or result of any other width is admitted: the call through a
  value carries the signature tag its type spells (`ssarc.signature_tag`, the
  spelling irlower's call sites carry), so wasm dispatches it through the
  funcref type the body was declared with. A void result is admitted too:
  every backend hands one word back from a void body (wasm types a void
  callee `(result i32)` like any other), so a `(K, V) => void` callback's
  call stands in statement position like any void call.

- The runtime INTRINSICS, typed as native's `FuncSigs` types them: the ten f64
  primitives `std/float` dispatches to and `__pow_f64`, the six bit counts, the
  raw-memory escape hatches (`__alloc`, `__alloc_u8`, `__free`, the loads and
  stores, `__memcpy` / `__memset`, `__ptr_width`, the heap marks), and the byte
  scans over a LENT string (`__sum_bytes`, `__ascii_run`, `__count_byte`,
  `__memchr`, `__rmemchr`, `__mismatch`).

  Each is a stack IR OP rather than a call, and a contract alone does not say
  so: without `ssarc.intrinsic_site` emitting the op, the name reaches the
  backends as a direct call to a symbol no runtime defines, which is a link
  error rather than a diagnostic. Contract and op go together.

  `__alloc_u8` is the one whose result the caller owns — a fresh zeroed buffer,
  counted like any other array. Every other answer is a scalar, and every
  argument is a scalar the op reads except the scans' string, which is lent.

  `__alloc_reuse` and the `__c_callN` trampolines are absent: the self-hosted
  IR does not lower them on any backend, so a contract would only move the
  failure from the bail site to the linker. That is a lowering gap behind the
  name, which is the half `TestSelfHostKnowsEveryNativeBuiltin` describes as
  self-reporting.

- `Map[K, V]` at a string or narrow integer `K` and a `V` the runtime's free
  family releases: a NARROW SCALAR column freed whole, a string column or a
  column of string arrays walked entry by entry, or a column of BOXES — a
  record, a union, an array of anything but strings, a tuple — walked with
  the value type's own release. A string key column is walked through
  the string dec (`__fern_map_free_ks` and its `_kvs` / `_ksvsa` / `_ksvf`
  members); an integer one holds no unit and is freed whole (`__fern_map_free`,
  `_vs`, `_vsa`, `_vf`). The `_vf` members take the release as a second
  argument: `__sem_release_<T>`, a body the physical lowering emits beside the
  drop helpers, which does for one value what the frame does for a unit of
  its type. A key column of records, a value column of function values
  (lent everywhere, owned nowhere) and one of maps (whose box a read could
  not retain) stay refused ("unsupported map shape").

  A map's unit is counted like any box's. The box carries the array header
  on the register backends (`__fern_map_new` takes it from `__fern_arr_box`)
  and the string box header on wasm, so a retain is the ordinary
  `__fern_rc_inc`, and the free family releases one unit: a decrement while
  the box is shared, and the columns and the block for the last. A consuming
  mutation — `insert`, `without` — runs on a box the frame is the only holder
  of: `ssarc.unshared_map` reads the count the receiver's unit is part of,
  and copies the entries into a fresh box first when it is shared, so a
  `snapshot = m` still reads what it held. The receiver's retain is never
  held back the way an array append's is (`deferred_retain`): a map the frame
  still reads counts as shared, and `var n = m.insert(k, v)` leaves `m` as it
  was, which is what E055 promises. Native, the interpreter and the AST
  lowering borrow the receiver and write a sole-held box in place instead
  (#9834); the production row `a-map-the-frame-still-reads-is-not-written`
  pins both answers.

  The vocabulary is `map_new(cap)`, `insert` (spelled `set` too, as the AST
  lowering admits both), `has`, `get_or`, `get` and `len`. An insert takes the receiver's
  unit and the KEY's, which the key column owns until the map is released;
  `kconsume` tells the runtime to hold that unit rather than retain it and to
  release the key an overwrite supersedes, and `owncols` makes the map the sole
  owner of both column buffers so a grow frees the one it replaced. An insert
  into a counted column takes the value's unit too, and the runtime releases
  the value an overwrite supersedes through the column's kind: the string
  dec, the string array's deep dec, or the release the op names (value kind
  3, the `_vf` members' function, reached through a pointer on the register
  backends and a table slot on wasm). A lookup borrows both operands and
  answers a scalar the map goes on owning; over a counted column `get_or`
  retains what it answers. A `get` answers an `Option` of the value in a box
  of the frame's own, released as any Option is; the runtime copies the
  column's entry into it without a retain, so over a counted column the
  lowering retains the payload on a hit, and the box owns one unit of it as
  any Option this frame drops does. `without`
  takes the receiver's unit and answers the map and a flag, releasing the
  removed entry's key and value through the columns' releases on the way
  (`__fern_map_delete_rel` on the register backends; wasm's delete reads the
  box's own column kinds); `cleared` is a
  fresh empty map; `keys` and `values` answer fresh arrays snapshotted from the
  columns, which is also how `for (k, v) in m` walks a map: the key column is
  what the loop indexes and the value column's element beside each key is the
  second binding (`docs/rc-log/2026-09-20-a-map-is-walked-through-its-two-columns.md`).
  The `op_map_iter` cluster itself is not admitted here.

  A map names no element in its construction, so the DESTINATION is the only
  place its shape is written: `map_new(2)` at an annotated binding or a
  contract's parameter produces, and one reaching a slot that spells no shape
  is refused. A literal desugars to a `map_new(n).insert(k, v)` chain, and
  an insert hands its receiver back, so the chain's head takes the
  destination's shape through the inserts.

- `Cell[T]`, the language's one mutable slot, as a VALUE and a declared field.
  It is a nominal name over a one-element box rather than a declared record —
  no declaration names it, and what it holds is one slot of its element — so
  every table that reads a nominal's schema reads a cell through that element
  instead. Physically it IS the one-element array `cell_new(v)` lowers to, so
  its box is released like an array's and its slot, when the element is a
  reference, walked by the same element loop. The element decides what a cell
  may hold: whatever this boundary can drop. A cell with no element — what an
  unannotated `var c = cell_new(0)` carries, since the destination names T —
  has no layout and is refused. The cell's own vocabulary, `cell_new` and
  `get` and `set`, is NOT here: a body naming one is refused at the callee, so
  a produced function receives, stores, projects and drops cells but never
  makes or reads one.

- A GENERIC declaration, as a TEMPLATE produced once per instantiation
  (`docs/SEMANTIC-GENERICS.md`). A declaration is generic when it declares
  type parameters or when a type variable appears anywhere in a spelling of
  its result, its parameters or its receiver — the parser's instance of
  `map[T, U]` at `T = i32` keeps `U[]` for the function argument to bind.
  Its contract keeps each type variable as
  `typeinfo.TypeErased`, a type of its own distinct from the unknown a checker
  failure produces, and a call site binds the variables structurally and left
  to right, one type per variable — a variable inside a function type, an
  array, a tuple, a map or a nominal type's arguments binds against the same
  shape on the argument's side. The call then names an INSTANCE,
  `fold_stmt_nodes$FwdScan`, produced under that binding on the call's
  request, and `build_module` runs the requests to a fixpoint. Inside an
  instance every type is concrete, so `T[]` is an array with a store width
  and a drop and `(ast.Expr, T, boolean)` a tuple whose element can be
  released; a parameter declared `own acc: T` is counted where `T` binds a
  reference and a value where it binds a scalar, and `own` in a function type
  drops at a scalar slot the same way. A closure body hoisted out of a
  template is instantiated under the enclosing body's binding. The template's
  own entry reports produced when every instance it was asked for did, and a
  template no produced body reaches is "uninstantiated generic". No produced
  value carries a variable: `ssasem.schema_error` refuses one as unresolved.
  In a module produced whole a template's erased body is SUPERSEDED
  (`irlower.LowerResult.superseded`): nothing calls it, since every produced
  caller calls an instance, so no emit writes it and no gate judges it — the
  AST lowering of an erased `__arrm_map__i64` clone carried the wasm route's
  only `erased_wide` verdict and declined the module (#9838). Under the
  bisect knobs an AST-lowered caller may still call the template, so there
  its AST lowering stands.

Refused, each with its own reason: calls of the remaining builtins, a void
call in expression position, an operator or a literal at the pointer width,
unsigned negation, generic records, the pattern shapes
above, receiver methods, external and async
functions.

A `defer` arrives here already lowered: the desugar replaces it with a flag
and replays its action at the scope's exits, lifting the declaration of a
binding the action names out of the block that holds it so the slot spans the
replay, and typing the shared return temp every expanded `return` writes. Both
need a value to start a declaration at, which `ast.ExprZero` supplies for any
annotation — the zero word, which at a reference type is the null the
assignment left at the declaration site overwrites, so the lift costs a slot
and not a box.

The lift and the return temp both take every annotated type, a function type
included: the temp is declared with the scope's result sidecars, so a closure
factory with a defer binds it a closure slot on both legs. An UNANNOTATED
declaration cannot be lifted either way — the desugar runs before the checker
and has no type to name.

## Calls

A call is verified against its callee's declared contract, never its body.
`ssasem.Contract` holds the exact parameter types, one unit mode per
parameter (value, borrow or counted, as the callee's own production reads
them from the declaration) and the result type. A declaration spelling no
result — a hoisted lambda — has the type the checker gives the first value
it returns, read in the scope the statements before it built, and is void
when it returns none. `build_module` derives one
contract per declaration this boundary can produce and hands the table to
every function; `ssasem.analyze` checks each call's argument and result types
against it, and the unit planner treats a counted parameter like a
construction operand (retained, or moved at the argument's last use), a
borrowed parameter as a read, and a reference result as a fresh unit of the
caller's own. A discarded call result is released at the call.

A value-position block is not a call at all. `parser.is_value_block` names the
zero-argument call of a zero-parameter lambda the parser desugars an
if-expression, a match-expression, a comprehension or a `{ … }` body to, and
this boundary INLINES it the way every backend does. The if-expression shape is
one `if` whose arms each `return` the block's value, joined at a phi. A block
with leading statements — a `{ … }` body, or the match-expression desugars that
route their value through a local declared ahead of a done-flag chain — runs
them in the enclosing block, in a scope of their own, and takes the trailing
return's value; a block whose last statement is none of an `if`, a `match` or
a `return` is refused.

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

## Self-tail recursion

A self-recursive call in tail position reuses its own activation on the AST
lowering, which gets `irlower.tco_self_tail` out of `lower_func`. A produced
body never reaches `lower_func`, so until #9692 it grew the stack once per
round and a deep enough recursion took the program out — on the DEFAULT path,
since `semlower.sem_ir_on` is true unless `FERN_SEM_IR` is set empty.

`ssasem.tail_recursion` rewrites it on the graph, before the unit planner and
the RC lowering see it, and that placement is the design rather than a
convenience: a tail call HANDS ITS ARGUMENTS OVER and a jump does not, so only
the planner knows which arguments carry a unit. Rewritten first, the loop is
an ordinary loop and the planner brackets it as one.

The entry block is SPLIT. It keeps its id and its index — the lowering reads
`blocks[0]` as the entry, and `ssadeps.verify` refuses a `param` instruction
anywhere else, so the parameter definitions stay put — and what is left
becomes a branch to a new HEADER holding the rest. The header takes one phi
per parameter, merging the incoming argument with the one each tail call
passes; every other use of a parameter becomes a use of its phi; each tail
call drops its call and branches to the header, and every block the entry used
to reach takes the header in the entry slot of its predecessor list so the phi
operands stay parallel. The pass runs before `ssasem.analyze`, so the verifier
checks what it produced.

Two shapes are DECLINED, and both are soundness preconditions rather than
conservatism:

- A tail call whose ARGUMENT is a view. The call kept the anchor alive,
  because the anchor is the caller's and the caller's frame stands until the
  callee returns; a jump re-enters the loop with the anchor already replaced
  by the phi. `irlower.rc_consumed_drop_wired` is the shape — it recurses on
  `slice_unchecked(t, 0, t.len() - 2)`, a view of the very parameter the
  argument replaces — and rewriting it corrupted the heap of every compiler
  built through this path. This declines the one BLOCK: a call that stays a
  call keeps holding what it holds.
- A function with a view PARAMETER. Every parameter gains a phi, the planner
  marks a reference-typed phi owned, and the entry edge then supplies it by
  RETAINING — which on a view is a no-op, since a view's box carries the
  immortal sentinel rather than a count, while the matching release frees the
  box. The loop would give back a unit it was never able to take. The
  asymmetry is the planner's (#9802); what this pass contributed was the only
  way to reach it, a phi whose operand is a borrowed parameter, since a loop
  that rebuilds its view each round merges fresh values and is correct.

The cost of the second is that a deep self-tail recursion on a `str` parameter
still grows the stack here while the AST lowering optimises it. It shows on
wasm, whose stack is small, and not natively.

## Validation

`internal/e2eselfhost/self_host_semsource_test.go`:

Both drivers run `irlower.lift_lambdas` over the module first, the way every
production backend reaches a tree it lowers. That is what puts a closure in
front of the boundary at all — the lift is where a lambda becomes a hoisted
body and a `__mkclo$` box — and it holds the two drivers to the same input the
census measures.

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
  result, a returned parameter, recursion, a call result carried into a loop
  header, the AST-lowered `main` binding, discarding and projecting tuple and
  array results under `ssarc.caller_sigs`, the byte and cast shapes below, the
  enum shapes above, the total-match shapes (a value-returning body closed by a
  match over a four-variant enum, over the same enum as an `own` parameter
  consumed arm by arm, and over a struct-union narrowed to its member, each
  called from a produced caller and again per step of a loop), every receiver
  the planner does not move (a borrowed parameter's box, a record field's, an
  element of a borrowed array of arrays, an `own` parameter's read again after
  the push, the field receiver appended to in a loop, and a `string[]` through
  both builtins, each asserting the source keeps its length and its elements),
  the loop header whose first step pushes on the caller's own box, and the view
  shapes: a scanning loop binding a slice per iteration and comparing,
  measuring, indexing and lending it to a `str` and to a borrowed `string`
  parameter; an owned string lent to `str` bindings and re-lent; a view of a
  view; a view whose source is a temporary whose only use is the slice; and a
  view receiver on a `string` method. The float shapes: a quotient sign-flipped
  and scaled, the three comparison answers, a loop-carried accumulator, an f64
  crossing a produced-to-produced call as a parameter and a result, and an i64
  converted to f64 and back. The function-value shapes: a bare name boxed into a
  trampoline and handed to a produced callee to be called there, a scalar and a
  reference argument lent across an indirect call, a temporary array literal
  lent to one, an indirect call whose counted result the caller owns and one
  whose result it discards, and a box carried through a branch join. The closure
  shapes are where the box's own unit dies: a capturing lambda lent to a
  produced callee and released after it, a loop building one per step, and a
  binding rebound on one arm of a branch so the join releases the box the other
  arm built. The wasm leg is what holds an indirect call to its ABI: a lost slot
  class there is an `indirect call type mismatch` rather than a wrong number.
  The cell shapes: a record and a variant payload each holding a `Cell[i32]`, a
  record holding a `Cell[string]`, every one constructed from a field read of a
  borrowed or owned holder, bound, matched and dropped, with the holder handed
  in as an `own` parameter so the cell's release is produced code's. The
  if-expression value blocks: a fresh array from either arm, a string from a
  produced caller, an `own` parameter handed to the join from the first arm and
  from the second, and a chained `else if`.

The generic fixtures are where the instances execute: a fold whose
accumulator is replaced once per call, one nested through a SECOND generic so
an instance requests an instance, and one carried through a loop phi — each
at a scalar instantiation and again at a `string[]` one through a lending
visitor, a consuming visitor and a lambda, with a heap value held ACROSS the
fold and read back after churn so an over-release answers a sentinel rather
than a length. The driver AST-lowers each template's own erased body for
main's calls and emits the instances beside it, so the two symbol conventions
link in one program. That arrangement has one trap worth knowing when writing
a fixture: a closure box built by the AST-lowered main and lent to a produced
callee is owned by neither, so it leaks the box under leakcheck. The
production compiler never mixes the two — a module takes one path or bails
whole (`FERN_STRICT_IR=1`) — so a fixture that wants a box at a borrowed
function slot builds it in produced code, the way `cap_fn` hands `dbl` and
`negate` to `via_cap`. The print golden pins the instance names, a template's
"template instantiated" verdict, a fold that reads an OWNED accumulator after
handing it to a consuming visitor (a retain in the `string[]` instance,
nothing in the i32 one), and one that abandons an owned parameter unconsumed,
dropped by the instance that knows its type.

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

A method on a generic receiver — `(o: Option[T]) is_some()` — is a template
whose variables are the names its receiver's arguments spell, and a call
through its `Type.method` contract instantiates it the way a folded array
or map method is instantiated: the receiver binds the variables and the
call names the instance. A method with type variables of its own on a
generic-struct receiver (`(h: Holder[T]) tagged[U](u: U)`) stays refused.

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
results, still rely on the AST caller's own syntactic rows, and the union one
is measured too: an AST-lowered `main` handing a produced callee's ENUM
result straight to another produced callee leaks both boxes — 80 bytes for a
`Pair(3, [3])` — so the executable fixture calls those shapes from a produced
caller, which balances. So does a borrowed record PARAMETER, and that one is
measured: when a produced callee RETAINS a
counted-element array field of one, the AST-lowered caller's release of the
record frees its box and leaves the field's buffer — 40 bytes for a
two-element `string[]`, 56 for a four-element one. Retaining the field into a
tuple is enough; no array builtin is involved, and the same program lowered
entirely by the AST pipeline balances.

Constructions over a loop element cross too (`bump_each`, `line_each`,
`word_recs`): a functional update and a variant literal built from the
element, an unannotated binding inferred from it, and a string element
retained into a record whose own unit dies at the end of the step.

The outcome of a write crosses too (`made_dir`, `wrote`, `unlinked`,
`removed`, and `saved` / `saved_exec` in the print fixture): `write_file`,
`write_file_exec`, `create_dir_all`, `remove_dir_all` and `remove_file` each
have a contract whose result is `Result[void, IoError]`. A `void` type
argument is a PAYLOAD that carries nothing, so the union's `Ok` variant has
no field — the same shape as `None` — and a pattern's one binding over it
names nothing, while `Err(e)` projects the IoError as it does off a read. The
box is the tag word with a zero behind it, which is what the writers hand
back and what a literal `Ok` at `void` builds. The RC leg runs the writers in
a directory of the test's own on every target, mapped in as wasm's one
preopen; the executable-bit writer is proved by the print fixture only, since
wasm grants no `fsmode` and a program naming it never reaches that emitter.

`target_os()` and `target_arch()` have no contract and never will: the driver
folds both to their literals before anything lowers, so the census folds them
the same way, against the default target, and counts the calls as the
literals the boundary actually sees.

The cell's own vocabulary crosses (`cell_count`, `cell_share`, `cell_words`,
`cell_wide`, `cell_float`, `cell_closure`, and `cell_round` / `cell_text` in
the print fixture). `cell_new(v)` is a construction over one element and
takes its unit; `get` hands out a UNIT of the element rather than a borrow of
the slot, since a write may release what the slot held while the value read
is still in hand; `set` takes the new element's unit, releases the old one
and gives nothing back, so it stands only as a statement. The box is the
one-element array the AST lowering builds, so a cell shared between a local,
a record field and a closure's capture is one box, and the write is seen by
every holder. Worth +6 outright against the 34 the vocabulary held: the
interpreter's cells all sit under its 32-bit float, which the fold had
already moved 24 more callers onto, so that leaf is now 47.

The 32-bit float crosses as above, worth +8 against the 47 it held: the
interpreter's float paths all sat under its string-builder handle, a `usize`.

The pointer width crosses as above, worth +64 against the 46 it held: the
whole interpreter produces, plans and lowers, and with it every evaluation
path the earlier leaves had moved one step further in. That leaf was the
checker's, not the boundary's: the spelling resolved to unknown, so every
declaration naming it was unresolved before any contract was read.

The map vocabulary crosses as above (`map_tally`, `map_words`, `map_eat` /
`map_hand` in the executable fixture, `seen_twice` and `flagged` in the print
fixture): a map built and grown in a loop over fresh, borrowed and element
keys, one key inserted twice so an overwrite releases the key it supersedes,
and a map handed to a callee that takes its unit. It was the last leaf, and
closing it took the count to every function the compiler has.

`ssasem.type_key` had no arm for a map, so one spelled what the fall-through
leaf does — `bool` — and `Map[string, i32]`, `Map[string, boolean]` and a
boolean would have shared one drop helper and one instance name the moment
maps crossed. A `char` spelled the same way, for the same reason: every
integer predicate here excludes it. Both have keys of their own now, and the
rejects fixture pins that a map and a boolean, and two maps of different
value types, spell three.

A closure's borrowed function capture crosses as above, worth +5 against the
5 it held: `astwalk.splice_stmts_with_lambda` builds a lambda over two of its
own function parameters, and its three callers came with it. The refusal it
replaces was the placement rule reading an environment record as any other
record, where a captured function value is the one field a record never walks.
A cell of a function value is refused with the elements now, which the rule
had missed: a cell outlives the frame that filled it, so nothing it holds can
be borrowed.

Measured against the whole loaded self-hosted compiler, **all 7,836 of its
7,836 functions produce, plan and physically lower**, through 107 instances of
its generic declarations. There is no leaf left, and no refusal of any kind:
the producer admits every function the compiler has, and neither the unit
planner nor physical RC refuses anything it admits.

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

The `Map` vocabulary was the last leaf (14: `map_new` 10 and the `insert`
/ `has` sites of `wasm_ir`); the closure env box, which stood at 482 captures and 84 bindings,
is at zero, worth +564 across the environment record and the `own` a lambda's
binding spells. Callees with no semantic contract got down to 47, none of them
a declaration: `util.append_all` (475) and the three `map_*_acc` walkers (39),
whose variable rode in an array and a returned tuple, closed outright when the
boundary began producing a generic body per instantiation, worth +424. The destructuring
declaration, the record literal, the cast and
operator contracts, the literal width and the escaping view are all at zero. The string view was
worth +355 once the sites that stored
one were made to copy, and the f64 +273 — each measured, against a leaf
histogram that had ranked them differently. The declared field width was worth
the 81 it was measured at — every construction with a wide field, `ir.Op`'s
f64 and i64 among them, resolved its declaration — and `.with` +233:
the builtin-call leaf was 453 functions of which 439 were a `.with`, which a
probe keyed by callee showed and the bare leaf histogram could not. The callee
leaf is the same shape: keyed by name it is a handful of runtime builtins
(`strbuf_append`, `__memchr`, `eprint`, `env`, `read_file`) and the `astwalk`
folds that take a function value, each with its closures behind it. The unit
planner refuses nothing now, and neither does the physical lowering: the last
14 functions that produced and planned but did not lower all carried an
`i64[]`, and an array's element ops each name their own slot width, so the
64-bit element rides the eight-byte stride wasm needs rather than being refused
for want of one. A tuple element stays narrow — `op_tuple_make` spells no
element kinds here — as does a closure capture. The flat tuple
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
callee, what was left after them was the function value — the `astwalk` folds
and the closures behind them — and the builtins whose result is an enum
(`env`, `read_file`) or whose vocabulary had not arrived. The record-literal leaf is
the same leaf in disguise: a probe keyed by the checker's reason showed every
one a field whose value is such a call. The literal tree, the
destination-typed literal and the string loop were worth +62 together: the
iterable leaf (67) closed outright and the two literal mismatches with it.
What was left of the binding-mismatch leaf after them was the lifted lambda
body reading its captures out of the untyped `__env` word array, which is also
what the closure form's reference-capture refusal reached from the other side;
the typed environment record closed both. A shift
whose count is another integer width — `n << k` with `n: i64` and `k: i32` —
was the operator leaf (52); the count now reaches the operator through a
`cast` to the value's width, which is the masking the runtime does anyway,
worth +41.

The three leaves at the widths and the string order closed together for +15
lowered, and each was a single shape a probe named outright rather than the
mixture its reason reads as. All 14 of the operator leaf were a string `<`;
all 18 of the cast leaf a same-width sign reinterpretation, `i32 as u32` in
the division-magic derivations and `i64 as u64` in the interpreter; the one
literal-width refusal a `2147483648u32`. The cast and the literal are one
piece of work, not two: admitting the conversion alone would have moved its
refusal to the next operator on the result, so the unsigned widths are
admitted as whole types — the operator forms that read no sign bit, the
extension that fills the high half with zeros, the u32's own arithmetic mask
and its text constant form. The +15 against a leaf of 33 is the usual shape:
most of the unblocked functions refuse again one leaf further in, which is
where the `f32_bits` callee leaf came from (3 to 10).

The total match was worth +10 lowered and closed the fall-through leaf (12)
outright, the other two moving one leaf further in to a closure callee. It is
a TERMINATOR rule rather than an admission of new vocabulary: all twelve were
a body whose last statement is a match covering every variant of its enum
with every arm returning, which needs no `return` after it because the value
is one of the variants. `ssasem.analyze` needed the matching rule in the same
move, since the closing arm projects its payload with no test above it — a
variant is now settled by a held test OR by every other variant of the enum
having been refuted. The verifier's cost is a third of the census's run time
(1m44 to 2m18), paid on the exclusion scan.

The refusal stays, because the rule does not cover a body native's E052
treats as non-falling for a reason this boundary does not model. One such
shape is measured: a `while (true)` no `break` leaves, which E052 accepts
and which still gets a live exit block here, the constant condition being
unfolded. No function in the compiler's own sources has it, so folding the
condition is worth a measured zero and was not built.

**Both binding-shape leaves were the closure ABI, not a binding rule.** Each
was probed before building and neither was what its reason read as:

- The 75 `binding type does not match its semantic value` were every one an
  `ExprIndex` initializer whose declared type is a reference and whose value
  is i32, and the binding NAME is the CAPTURE's (`$binding$1$name`,
  `$binding$5$mfuncs`), never `__env`. `irlower.make_clo_func` writes
  `var cap: T = __env[1 + i]` with `__env: i32[]`: the declaration carries
  the capture's real type over a box slot the AST lowering treats as an
  untyped word, which is a reinterpretation Fern has no operator for. The
  declared type is the truth and the READ is the lie, so the fix is a typed
  env representation — a per-closure schema the box is built and read
  through — and not a relaxed check at this boundary. It is NOT the erased
  accumulator's decision, measured: admitting the erased word left this leaf
  at exactly 75.
- The `unsupported destructuring declaration` leaf (61) was not a pattern
  shape at all. A probe keyed by the pattern text, the destructuring marker
  and the initializer's type found every one a FLAT tuple pattern — no nested
  position, no struct form, no `@` binder, no discard — refused only because
  `tuple_arity` of an unknown is -1, the unknown coming from a call whose
  fn-valued argument is a `__mkclo$…` env-box marker. The checker types that
  marker now (below) and the leaf closed, MOVING as predicted rather than
  paying: all of it calls a generic `astwalk` walker (`map_expr_acc`,
  `map_stmt_acc`, `map_stmts_acc`) whose accumulator is an erased type
  variable, which is the fold family's own refusal one leaf further in.

The two small leaves beside them are the instantiated builtin union. The
match scrutinee (8) is `Option` alone and the unresolved result type (3) is
one `Result` and two erased `T`s: `Option` and `Result` are injected without
declarations — `Some` / `Ok` / `None` / `Err` are special-cased in the
emitter — so there is no `UnionSig` to read a layout or a variant's field
shape from, and a payload is the scrutinee's type ARGUMENT rather than a
declared field. That is the same vocabulary `env` (158) and `read_file` (26)
need.

The no-contract refusal names its callee now, so the census splits that
leaf by builtin on its own: the `astwalk` folds that take a function value
(some 549 functions between them), `env` (158), `read_file` (26), and a
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
operator's width. What was left of the record-literal leaf was three sites and
no record shape: two a field whose value calls a runtime builtin the self-host
CHECKER had no signature for (`string_from_bytes_unchecked`, `f64_bits`), so
`check_expr` handed back the unknown that collapses the literal, and one a
field whose value is a call taking function values.

Typing those two in `check_expr` took the record-literal leaf from 74 to the
one function the third site holds, and it was not a record change: the leaf
became `array element type` at 72 on the way,
because the byte-array argument both builtins are handed was written `[b]`
over an `i32` local against a `u8[]` parameter. Fern has no implicit numeric
conversion, so the boundary refuses the element rather than narrowing it on a
guess; the two lexer sites now write the byte they mean (`b as u8`, and a
digit buffer declared `u8[]`). The 74-function leaf was worth +28 lowered —
the rest refuse again at the function-value and closure leaves. Retyping a
builtin call does move the twelve diagnostic walkers with it: measured over
every `.fern` file in the repository, the single-module checker driver reports
three further `E043 no method` lines, each on a call whose receiver is now
typed and whose method lives in a `std/*` module that driver does not resolve
— the same false positive that driver already reports 15 times in
`utf8_codepoints` alone.

`Cell[T]` was the whole variant-field leaf: a probe keyed by union, variant
and field showed all 98 functions were `interp.Value`, whose `VCellI` payload
is declared `Cell[i32]` and resolved to unknown, so every function naming that
union lost its schema. The self-host checker now resolves the annotation to
the reserved builtin struct carrying its element, the shape native's
`builtinStructDecls` registers, and types the cell's two methods beside the
map's. A cell is NOT a declared record: no declaration names it, and what it
holds is one slot of its element, so `semrecords.resolved`, `schema_of`,
`ssaunits.supported` and `ssarc.supported` read it through that element. Its
box IS a one-element array box — `cell_new(v)` lowers to `[v]` — so the drop
walks the slot with the array machinery and releases the box the way an
array's is released. That was worth +51 lowered of the 98, measured; the
other 47 refused again one leaf further in.

**The AST lowering does not reclaim a cell-typed struct field at all**, which
is the self-host half of `docs/CELL-TYPE-PLAN.md` §RC and is unrelated to this
boundary — a pure-AST `struct Slot { c: Cell[i32], n: i32 }` bound in `main`
leaks 32 bytes under `FERN_LEAKCHECK` where native balances, as does a local
`Cell[string]` whose slot holds a heap string. Admitting the field to the
struct-drop walk alone is NOT the fix: the construction side takes no count
for a cell field either, so a cell aliased into two structs turns the leak
into a use-after-free under the sanitizer (measured). The executable fixtures
therefore hand every cell-bearing holder straight to an `own` parameter, so
each one is released by produced code.

The cell fixture also found a lowering bug of its own, since fixed
(`internal/e2eselfhost/self_host_variant_name_shadow_test.go`): a variant
whose name a plain struct also declares had its match arm read the payload
through the STRUCT. An arm resolves its owner from the pattern's `Enum.`
qualifier and falls through to the scrutinee only when no decl of that name
carries that owner — but an absent qualifier is the empty string, and a plain
struct's enum_owner is the empty string too, so the fall-through never ran.
The struct's field 0 is not `__ev`, so the arm took the struct-union member
form, bound only its first binder and left the rest unbound; an unbound name
lowers as a function ADDRESS, which is a symbol nothing defines. The fixture
named its variant after a struct already in the same module by accident, and
the lift that reaches this boundary now put the two in one module.

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
`fieldPlaceAppendCopies` inverted) exempts a site; the analogue here is
`ssaunits.field_grow_root` — "The field append, admitted", below.

Next, by measured leaf: the callee leaf is two things, a contract for the
runtime builtins a body calls, which is a vocabulary question and not a leaf,
and the function value, whose shape half the indirect call form above closed.
That was worth +3 lowered, which is the measurement and not the histogram:
the fn-value leaf is almost entirely the `astwalk` folds, and those are
refused for a reason a form for the ADDRESS does not touch.

The closure CONSTRUCTOR was worth +49 lowered against a leaf of 254 across 90
distinct `__mkclo$` names, and the shortfall is the honest part of the
measurement. A probe naming the higher-order callee each closure is handed to
ranked those 90 before the form was written: the payable ones go to
`wasm_ir.any_op`, `parser.map_expr_kids` and `astwalk.map_stmts` / `map_expr`
/ `map_stmt`, all concrete and already produced. Of the 254, 186 refuse one
leaf further in at a capture that is a reference, 15 at the `astwalk` folds
below, and 4 at a record literal or a destructure; the leaf histogram alone
ranked the constructor fifth and could not see any of that. The
reference-capture half is not a form this boundary is missing: the hoisted
body refuses the same shape from the other side, so admitting the constructor
there would move the census by nothing while making the box own a unit nothing
releases.

Admitting the box settled the ABI question the address form had left open.
`irlower.lower_func` marks every fn-typed parameter of a free function a
closure local and dispatches a call through it environment-first, and the lift
boxes every fn-value argument to match, so a produced body emitting the
bare-address call disagreed with the AST lowering for the same parameter:
`wasm_ir.any_op` produced and lowered a `call_indirect` on what is a box
pointer at run time. No production consumer reads these bodies, so nothing had
executed it. One function type carries one ABI; the box is the one the only
program this boundary reads has. The address form went with it, measured at
zero.

**The `astwalk` fold family produces, plans and lowers.** All nine members —
`fold_expr`, `fold_stmt`, `fold_expr_pruned`, `fold_stmt_pruned`,
`fold_expr_nodes`, `fold_stmt_nodes`, `fold_stmt_spine`, `fold_stmt_own`,
`fold_stmt_own_pruned` — and the `$wrapN` trampolines the lift builds inside
them, once as a TEMPLATE and then once per instantiation the compiler's own
callers bind (`docs/SEMANTIC-GENERICS.md`). A function TYPE spells its
consuming slots, the visitors declare `own acc: T`, the `$wrapN` trampoline
mirrors its target's modes, and the AST lowering reads the same mask off the
callee's type at an indirect call — so a call through a function value and a
direct call to the same signature agree on who releases the argument, and
`own` at a slot a scalar binds drops out of the type on both sides.

The family first lowered under an ERASED rule — one body per template, every
erased position a move the unit planner proved — which bought the scalar
instantiations, then with the consuming convention the reference ones, and
stopped at a variable under a CONTAINER: `util.append_all[T]`'s `T[]` and
`astwalk.map_expr_acc`'s `(ast.Expr, T, boolean)` were refused as unresolved,
because releasing an array or a tuple walks its slots and an erased slot names
no drop, and 514 functions stood behind those two. Producing an instance per
binding instead — what `internal/monomorph` does for native — dissolved the
question rather than answering it: inside `append_all$arr$FuncDecl` the
element is a record with a drop, and the erased rule, its proof and its two
runtime alternatives (boxing every erased value, or a drop word beside it)
left with it. Measured: 6,732 produced before, 7,156 after, +424 against a
leaf of 514, with 60 instances requested and every one produced.

The builtins whose result is an instantiated builtin union were worth +82
lowered, against a leaf of 215 that ranked them at twice that. A probe —
close_module told to ignore the cascade rooted at those callees — measured
the ceiling BEFORE the work: 37 transitive callers, plus the 37 direct ones,
because 124 of the leaf's 158 `env` functions refuse again one leaf further
in at the `astwalk` fold family. The rest of the delta came from two shapes
the admission exposed rather than from the contracts: the total match read
off a builtin union's positions and an injected enum's variant structs
rather than off a UnionSig alone, which is what carries a `match` on either
of them. `env`, `read_line`, `read_file`, `read_dir`, `args`, `putchar`,
`f32_from_bits`, `__rc_underflow_count` and `Some` all closed outright, and
so did the match-scrutinee refusal.

**The call-target leaf was the value-position block, and the histogram said
nothing about it.** A probe keyed by the SHAPE of the refused target put all 49
on two causes and neither was the closure env box the note here had guessed:
34 were a callee that is a LAMBDA — every one a `vb:if_expr` value block with
one statement and two arm returns — and 15 were `Cell` and `Map` methods
(`.get`, `.set`, `.insert`, `.has`), which are the vocabulary question the
`map_new` / `cell_new` callees are. Not one was a bound name holding a
non-function, a `.len` on an unsized receiver, a slice of a non-text source or
a builtin with type arguments, the six other sites that produce that reason.

A value-position block is a zero-argument call of a zero-parameter lambda the
parser marks with an origin, and every backend INLINES it
(`irlower.lower_value_block`): the statements run in the enclosing function and
only the last is the block's value. The if-EXPRESSION desugar puts one `if`
there whose arms each `return` that value, so this boundary produces it as a
branch whose arms join at a PHI rather than at a terminator — the arm's leading
statements run in the arm's own block, where a `return` is the enclosing
function's as the inlined form means it to be, and an `else if` nests one arm
inside another. The value phi is a unit of this function's own, supplied on each
incoming edge exactly as a binding's phi is. It was worth +34 produced, of which
the leaf histogram could see none; nearly all of them refuse again at the
reference capture, which is why the lowered delta is smaller. Every other block
origin — a general body, a comprehension — stays refused, and the checker
resolves the type of none of them either.

The checker now types the lift's closure constructor. `__mkclo$<body>(caps)` is
the box holding `<body>`'s address and the captures that body reads back out of
its first parameter, so the value's type is what `<body>` promises MINUS that
parameter; `parser.mkclo_body` holds the spelling for the checker, this boundary
and the lift's own readers. Without it the box types to unknown and collapses
whatever expression holds it: that was the whole record-literal leaf (2, a
`FuncDecl` update whose `body` field calls `astwalk.splice_stmts_with_lambda`
with two lifted arguments) and the whole destructuring leaf (61). The marker can
only appear in a tree the lift has already walked, which no production checker
run sees, so nothing else reaches the rule.

The one `view result escapes its source` was a real escape and the source now
copies. `arm64_gas_bcond_suffix` returned a window of its own `string`
parameter, whose bytes the caller owns and may outlive the result; it returns an
owned copy, which is `docs/STR-VIEW-CONTRACT.md` §5's decision for every view
producer. That closed its four callers' no-contract leaf with it.

What is left of the leaves this pass took: the remaining `unresolved parameter
type` (4), `unresolved result type` (1 of 4) and `unresolved binding type` (1)
are all `usize`, which the self-host checker deliberately resolves to unknown
(a pointer-width integer is a target-dependent width decision, not a boundary
one); one result is `Result[void, IoError]`, a union instantiated at `void`,
which the builtin-contract leaf needs anyway; and two are `$wrap0` trampolines
of the `astwalk` folds, which stay refused for the reason below.

The container of a type variable is at zero: a generic body is produced per
instantiation, so `util.append_all` and the `map_*_acc` walkers lower at every
type the compiler binds them to, and the 514 functions behind them with
them. Read the net: the leaf was 514 and the census moved +424, because 90 of
those functions refused again one leaf further in at the closure env box,
which went 389 -> 482.

The closure env box is at zero too, worth +564 against the 566 it held. The
environment record bought the captures (482 -> 0) and moved the binding leaf
84 -> 130 -> 107 before it closed, each step a shape a probe named: a lambda
or nested function with an OWNING parameter bound to a name, whose binding
the checker typed without the `own` its body declared (three sites, plus the
box type read off the hoisted signature with its flags one slot out), then
one whose owning parameter is an ARRAY, whose `own` sits on the spelling's
base name under the array suffix and which `parser.ref_is_own` answered false
for. Each of those was a checker or lift bug the boundary's exact-type rule
found, fixed where it lived rather than relaxed here.

What is left is 14 functions, all of them the `Map` vocabulary: `map_new`
and the `insert` / `has` sites. Each backend's map runtime manages a value's
ownership differently — the register runtimes do not refcount a value per
insert and settle it at reclaim, wasm retains every pointer value it is not
told to consume — so a contract naming what a call takes and gives back is a
runtime question before it is a boundary one, and `op_map_set` carries six
flags that say so. The void-`Result` builtins and `target_os` closed, worth
+10 against their 26, the cell vocabulary +6 against its 34, the 32-bit
float +8 against its 47, the pointer width +64 against its 46 and the
borrowed function capture +5 against its 5: each moved the interpreter's
callers one leaf further in, and the pointer width was the last of them. The
production consumer that lowers produced functions through this pipeline and
feeds `caller_sigs` to the remaining AST callers is below.

## The production consumer

`examples/self_host/semlower.fern` is where a whole-program emit path asks for
this pipeline instead of a test driver. It is the default; `FERN_SEM_IR=` (the
empty value) turns it off, and `FERN_SEM_IR_REPORT=1` prints a line per
refusal and a per-module tally. Off, a backend receives what it received
before, op for op — the substitution is the only thing the path adds. On, a
module is produced whole or not at all, and `ircore.lower_gated` reads the
produced bodies in place of lowering them, so the AST lowering's verdict is
asked only of a module that fell back to it.

All three whole-program paths take one: `asm_ir`, `asm_arm64_ir` and `wasm_ir`
each gained a `_sub` sibling of their gated entry that threads an `ircore.Sub`
through it, and the old name calls it with `ircore.no_sub()`. The registry set
inside comes from that value rather than from the caller's own base, since
`caller_sigs` rewrote rows in it and the emit has to read the same set the
lowering did.

**The substitution is passed as a VALUE, and that is a size decision rather
than a taste one.** Calling the producer from inside the three backend modules
made every driver that links a backend link the pipeline behind it —
`semsource` + `ssarc` + `ssaunits` + `ssasem` + `semrecords` + `semtypes` +
`ssalayout` — for a flag it never reads. The driver-size gate (#6826) measured
eight drivers growing **6.7% to 10.2%**, among them `wasm_run`, `asm_run` and
`wasm_runio_run`, none of which can select the path at all. With the seam
inverted those three link **4,096 bytes** more than main, one page of
alignment, and the CLI carries the 601 KB alone.

Three rules hold a mixed module together, and the third is the one that was
not obvious:

- **An AST-lowered caller of a produced callee** reads registries derived from
  that callee's syntax. `ssarc.caller_sigs` rewrites them from the verified
  types, and the AST lowering runs with the rewritten set — the contract this
  file's §"The caller contract at the AST boundary" describes.
- **A produced caller of a produced callee** is the shape the executable
  fixture already proved.
- **A produced caller of an AST-lowered callee has no repair at all**, because
  the callee's parameter modes are whatever the interprocedural fixpoints
  concluded rather than what its declaration says. So such a body keeps the AST
  lowering, and dropping it takes its own callers with it: `semlower.prune` is
  that fixpoint, run over the produced bodies' `call_direct` and `const_func`
  operands with the runtime helpers, the C calls and this boundary's own
  `__sem_drop_*` set excluded. The same body can arrive as a FUNCTION VALUE
  an AST-lowered caller built and handed down, which no operand names, so the
  fixpoint also turns off a produced body that calls a value of a type an
  AST-lowered hoisted body (`$wrap`, `$clo`, `$iife`) has — by type rather
  than by flow, the safe direction — and, in the other direction, a produced
  hoisted body whose creator is AST-lowered together with everything it
  reaches by direct call: the AST caller of a box cannot see through the
  call, so nothing below it may consume what that caller lent (#9414,
  `rc-log/2026-09-16-a-function-value-the-ast-lowering-builds-is-not-called-by-a-produced-body.md`).

The whole-module refusal that preceded the fixpoint was worth measuring: over
the conformance corpus it left 190 of 498 modules producing NOTHING, because
one produced body reaching one refused stdlib leaf took every sibling with it.
The per-body fixpoint takes the same corpus to 15,475 of 17,468 declarations
emitted from produced bodies, in all 498 modules, and the wide tuple element
below takes it to **16,743 of 17,468 (95.8%)**.

### What the corpus measured, and the four defects it found

Every conformance case with an `x86_64` backend, no stdin and no expected
error — 498 of them — compiled twice by the same binary, once with the flag
and once without, both run and compared: **496 answer identically, and the two
that do not fail identically on BOTH paths** (`alloc_flat_bytes_roundtrip` and
`contains_demo` are self-host divergences that predate this and are unrelated
to the substitution).

Getting there is the part worth recording, because the first differential was
green and measured nothing. `FERN_SEM_IR=` — the empty value a harness writes
for its control leg — read as "set, therefore on", so both columns were the
semantic path. The flag treats an empty value as off now; the rule for any
flag added beside it is the same.

With a real control, four defects showed, and three of them were one cause:

- **An unsuffixed integer literal past the i32 range, with no destination to
  settle it, was read at the default width and truncated to nothing.**
  `4611686018427387904 as i64` names its width through the CAST, whose operand
  `semsource.cast` produces with no expectation at all. It is an i64 now, which
  is what the checker's own settling would say. That one change fixed
  `narrowing_cast_of_expression`, `int_byte_swap` and `u64_field_array_with` —
  the last two through a `17179869191 as u64` and a `72623859790382856 as u64`
  that had been silently zeroed.
- **A mutable capture is a `Cell[T]`**, on both lowerings (#9320). `capturebox`
  rewrites a captured local the closure or its creator reassigns into
  `var $cell$x: Cell[T] = cell_new(init)`, every read into `$cell$x.get()` and
  every write into `$cell$x.set(v)`, and the lambda captures the cell. The
  box is the one-element array box a cell already is, so nothing changed in
  the representation; what changed is that the write is the cell's in-place
  `set`, which both paths lower as the element store (releasing what the slot
  held), rather than an array `.with` whose in-place arm depended on a count
  that happened to be one. The boundary's earlier refusal of the write
  (`replacement of a capture`, 643 declarations across the corpus, since a
  refused `for_each` callback keeps the whole test file on the AST lowering)
  is gone with the spelling. The capture the CLOSURE writes is a scalar by
  E049, and every scalar is boxed at its own type: the box scan used to admit
  only `i32` and `boolean` there, so an `i64` or `f64` the lambda wrote was
  captured by value and the writes were lost, on both lowerings.

  `State.foreign` still records the values naming storage this frame did not
  create — a projection of a lifted body's environment record — and `assign`
  refuses a replacement of a binding holding one; a mutated capture never
  reaches it. The earlier reading that a creator's write was sound because
  "the box is its own at the write" was wrong for a closure that ESCAPES:
  the creator's `.with` then went into a copy the environment never saw, and
  the produced program read the creation-time value (103 for native's 104
  on a rebound string). The cell's `set` closes that too.

### The wide tuple element: the biggest leaf was a construction site

The first census ranked `unsupported physical RC value type` at 1,032 refusals,
four times the next leaf. A probe by name put all of it on 22 distinct
functions: the 128-bit multiply helpers behind the float and string
shortest-representation work (`float.__db_mul64`, `string.__el_mul64` and their
callers) and `bigint.__bi_divmod_mag`. Each returns a TUPLE with a 64-bit
element, which this boundary refused because "`op_tuple_make` spells no element
kinds, so a wide tuple element has no store width to be written at".

That was true of `op_tuple_make` and not of the IR: `op_tuple_make_k` carries
the comma-joined element kinds, the AST lowering has emitted it for years, and
the wasm backend picks `i64.store` / `f64.store` / `i32.store` out of it. The
READ side of this boundary had named its width all along
(`op_tuple_get_w`), so the construction was the only half missing. `ssarc`
emits the kinds now (`tuple_kinds`) and the element admission drops the
narrowness test; `narrow_slot` keeps its meaning for the one undeclared
construction that still writes an i32-shaped word per operand, the closure box.

Worth **+7.2 points of the corpus: 15,475 of 17,468 declarations to 16,743 of
17,468 (88.6% -> 95.8%)**, with the conformance differential still at 496 of
498. The cascade went with it — "calls a body the AST lowering defines" was the
second leaf at 236 and is out of the top ten. `wide_pair` / `float_pair` in the
executable fixture pin a 64-bit and an f64 element, each beside a narrow one
and the f64 beside a string so the drop walk crosses the same box, balanced on
every target.

### The compiler compiling ITSELF through this path (#9328)

The corpus agreeing 496 times is not the same as a program agreeing. The
self-hosted compiler compiles itself through the production consumer — **7,525
of 8,246 declarations and 64 of 64 instances produced**, 6m31s, a 12.82 MB
binary — and the compiler that comes out reads a file, compiles it, and the
program it emits runs. That took one defect, below.

It is not yet the fixpoint: handed the whole self-host tree, that compiler
segfaults 23 s in, so something in the produced lowering is still wrong at a
scale the small programs do not reach. Before the defect below was fixed it
segfaulted on the FIRST file it read, so the remaining one is a different
bug, not the same one half-fixed.

Before it was fixed the same build succeeded and the binary **segfaulted on
anything that reads a file**, with its no-file modes (usage, `-targets`)
working. That is the shape `docs/TEST-GATES.md` warns about, arriving on
schedule: a green corpus and a stable miscompile. What found it was not a suite;
it was running the thing.

`FERN_SEM_IR_ONLY` and `FERN_SEM_IR_SKIP` — prefix lists, the second an
exclusion — are the bisect knob. Halving with ONLY isolates nothing here,
because either half prunes the call between a produced caller and a produced
callee; removing one prefix at a time from the whole set does. Against a
lexer-plus-parser probe rather than the whole compiler, which takes the loop
from six minutes to ten seconds:

- The segfault is heap-layout sensitive — 36 different single-function
  exclusions each "fix" it — so it is a poor oracle. `FERN_SANITIZE=1` at
  compile time gives a deterministic one: **use-after-free (touched a
  quarantined block)**, where the AST build of the same program reports only
  the known leak.
- Under that oracle the exclusions that clear it are all in the lexer, around
  `scan_number` and the chain it calls. Its `slice_unchecked(l.src, begin, l.i)`
  is the view. Read those exclusions as a CONE rather than a cause — see the
  note on `prune` below.

The cause is that a callee lent a view may keep the BOX. A view's box carries
the immortal rc sentinel, so the retain the callee owes for a borrowed reference
it keeps is a **no-op**, and the produced caller then reclaims the box out from
under it — the frame that sliced the view and whatever holds the reference
release one box twice. Four lines reproduce the smallest shape:

```fern
function keep(text: string): string { return text; }
function scan(src: string): string {
    var v: str = slice_unchecked(src, 0, 3);
    return keep(v);
}
function main(): i32 { return scan("12345 abc").len(); }
```

x86-64, `FERN_SANITIZE=1` at compile time: the AST build exits 3 and reports
only the known leak, and `FERN_SEM_IR=1` faults. It is a fault of the produced
lowering alone, which is what makes it the check the eventual fix answers to.

Three shapes separate it, and the separation is the finding: a callee returning
a **scalar** is clean, a callee returning a **fresh** string is clean, and only
the one that hands the box back faults. What the callee does with the box is the
whole of it, and the signature does not say.

**Three fixes were tried and reverted**, each ruling something out:

- *Refuse `lend`'s retag where the callee's result is text.* Closes the
  reproducer, wrong rule: the danger is a callee that hands the value back,
  which is a property of the body. The executable fixture holds the
  counterexample — `lent_views` calls `copied()`
  (`function (s: string) copied(): string { return s + ""; }`) on two lent
  views, a text-returning callee whose result is FRESH — and the refusal took
  it out of production. It does not close the class either; the lexer probe
  still faults.
- *Use `__fern_str_view_free` for every string release.* Correct as a superset
  and fixes nothing: the fault is a box freed TWICE, which the view-aware
  helper does as readily as the plain one. The fix below goes the other way at
  the sites that need it — the PLAIN release, which stands down on an immortal
  box where the view-aware one reclaims it.
- *Copy a text result of a call that lent a view* (`v + ""`). Verified to fire,
  and it makes the fault worse — the un-copied call result is still a value of
  its own, so the plan drops it, which is the second release. Adding an owner
  cannot fix an over-count.

The fix is that such a callee KEEPS what it is lent, so it is handed something
it may keep: a copy. Two facts make that precise.

The first is which callees keep it. Neither the signature nor the graph shape
separates "keeps the argument" from "builds something new" — only the body does
— so `semsource.handers` reads the bodies: a parameter escapes when it reaches a
`return` as itself, inside an aggregate the return builds, or as an argument of
a call to a declaration that already escapes its own, closed to a fixpoint over
the module. `escapes_in` is that predicate and it is deliberately narrow: `s + ""`
and `s.len()` read the value and build something of their own, which is what
keeps `lent_views`' `copied()` calls reclaiming the views they are lent. The
container builtins — `append`, `with`, `insert`, `cell_new` — and the union
constructors count as keeping, because `return acc.append(w)` hands `w` out
inside the array. `semsource.lend` consults the set: where the callee is in
it, a view is not retagged but COPIED (`v + ""`, an owned string), and that
copy is what the call is handed.

Returning the box is only the smallest shape of this. The self-host lexer's
`number_tok`/`quoted_tok`/`ident_tok` each store a lent view in the Token they
build, which is why skipping any one of them left the probe faulting and only
the whole group cleared it.

The second is what the copy settles that a release could not. The box is one
half: a counted box is what a keeping callee's retain really retains, on every
backend, so the caller's release of its copy leaves the callee's reference
standing. The BYTES are the other half, and the one a release cannot reach: a
view's bytes belong to its source, and where that source is a string the
slicing frame owns — `constfold.lit_int`'s `slice_unchecked(s, 1, s.len())`
over the `s` it built — the frame releases the source on its way out and the
stored view reads reissued memory. That was the wrong assembly of #9407
(`rc-log/2026-09-15-a-lent-view-is-copied-for-a-callee-that-keeps-it.md`):
the sanitizer is silent on it, because nothing touches a freed rc, only bytes.
The first form of this fix released the handed box with the counted release
and left it at that, which closed the box's double free and not the bytes.

The AST lowering leaks the source instead, which is the other way to keep
the bytes alive; `TestSelfHostSemanticProduction` compares the sanitizer's leak
figure between the two columns and refuses a produced body that leaks MORE
than the AST one, so the copy reads as the produced bodies freeing more.

A guarded release — a pointer compare emitted around the drop — was built first
and does not work: on wasm the two pointers are equal and the retain was real,
so skipping the release leaks, and the guard has no way to ask which world it is
in without reading the box it may already have freed.

**A note on the bisect.** `semlower.prune` turns off every produced body that
calls one this boundary is not emitting, to a fixpoint, so `FERN_SEM_IR_SKIP` of
a LEAF removes its whole caller cone. That is why three unrelated-looking
exclusions (`advance`, `advance_to`, `at_end`) each cleared the lexer probe: they
are the cone, not the cause. Read a clearing exclusion as "the fault is inside
this cone", and intersect cones rather than trusting the smallest one.

### What stops it being the FIXPOINT: memory, not answers (#9365)

The compiler this path builds answers correctly on small programs and segfaults
23 s into the whole self-host tree. The reason is not a wrong answer, it is
**peak memory**. Both compilers below are built from the same sources by the
same self-host compiler; the only difference is `FERN_SEM_IR`:

| input | AST-lowered build | semantically lowered build |
|---|---|---|
| `manyf.fern` (200 declarations) | 6.9 MB | 43.3 MB |
| `examples/self_host/lexer.fern` | 134 MB | **11,550 MB** |

It is not a leak. Under `FERN_LEAKCHECK=1` on `manyf.fern` the semantic build
**frees more and leaves less live** than the AST one — allocs 196,745 / frees
155,417 / 3.1 MB live, against 154,810 / 38,842 / 7.4 MB. So the memory is
churn the allocator cannot recycle, not data the program still holds.

The growth is superlinear: 12x the input for 268x the memory, which is the
signature of a per-item allocation whose SIZE grows with the item count, freed
into a size-class freelist no later request matches. The runtime's freelist is
indexed by exact word count (`__fern_freelist`, one class per 8-byte word up to
512 KB), so a free of N words satisfies only a later request for exactly N.

What it is NOT, measured: appending to `i32[]`, to `string[]`, to a struct
array, and concatenating a string in a loop all match the AST build within
noise. The front end is not it either — the lexer-plus-parser probe built
through this path peaks at 30 MB against the AST build's 107 MB on
`parser.fern`. That leaves the back end.

The bisect knob is `FERN_SEM_IR_SKIP` with peak RSS as the oracle, one rebuild
of `fern.fern` per step (about 7 minutes each). Module functions are matched by
their BARE name, so `lexer.` matches nothing and single letters are the coarse
cut:

| skip | peak | exit |
|---|---|---|
| `a` … `m` | 11,566 MB | 0 |
| `n` … `z` | 26 MB | 139 |
| `n,o,p` | 11,564 MB | 0 |
| `q,r,s` | 11,554 MB | 0 |
| `t,u,v` | 30 MB | 139 |
| `w,x,y,z` | 134 MB | 0 |

The last row is the AST build's own figure, so `w`-`z` is the cone — the x86
assembler, whose hot path is `x86_emit_mem(buf: i32[], …)`: append through a
BORROWED parameter. Eight lines reproduce it, and the reproducer is the thing
to work against rather than the 7-minute rebuild:

```fern
function push(buf: i32[], v: i32): i32[] { return buf.append(v); }
function build(n: i32): i32[] {
    var out: i32[] = [];
    var i: i32 = 0;
    while (i < n) { out = push(out, i); i = i + 1; }
    return out;
}
function main(): i32 { return build(40000).len() % 97; }
```

x86-64: the AST build answers in 2 ms; the produced build takes **23 s and
8.9 GB**. Every push copies the whole buffer.

**The mechanism.** The append gate of the time, `ssarc.sole_owned_base`, asked
`__fern_rc_is_unique` to decide between growing in place and un-sharing — but
the receiver's Supply had already emitted `__fern_rc_inc` before it, because
`supplies` runs before `instruction`. The count the test read was the one the
test's own supply had just added, so it answered "shared" for a box nobody else
held, every time:

```
call __fern_rc_inc          # the Supply
call __fern_rc_is_unique    # now 2, so never unique
  ...  __fern_arr_dec ; __fern_arr_slice    # un-share copy of the whole buffer
call __fern_arr_push_owned
```

None of that is the append path now. `sole_owned_base` is `with_update`'s only
caller, and `ssarc.append_push` runs the `__fern_rc_is_unique` gate itself: the
consuming `__fern_arr_push_owned` when unique, and the non-consuming
`__fern_arr_push` when shared, whose hand-asm un-shares with one memcpy instead
of the per-element `__fern_arr_slice` loop above (#9600,
`rc-log/2026-09-17-a-shared-receiver-is-the-runtimes-copy-to-make.md`).

The AST lowering has no such test. It calls the shared, NON-consuming
`__fern_arr_push`, whose own gate grows in place at rc 1, and retains the result
only when the pointer comes back unchanged:

```
call __fern_arr_push
cmp result, buf ; jne .skip ; call __fern_rc_inc
```

**Two shapes that are NOT the fix, both tried.** Moving the retain into the
unique arm leaves `push` growing through `__fern_arr_push_owned` at rc 2, which
re-gates and copies anyway — the reproducer only improves to 3.5 s and 6.4 GB —
and the same reordering applied to `with` is a MISCOMPILE, because `arr_set`
mutates unconditionally. This returns 199 on the AST path and 15 with the
reorder:

```fern
function set0(buf: i32[], v: i32): i32[] { return buf.with(0, v); }
function main(): i32 {
    var a: i32[] = [];
    a = a.append(1);
    a = a.append(2);
    var b: i32[] = set0(a, 99);
    return a[0] * 100 + b[0];
}
```

Inferring the MODE instead — a reference parameter an `append` consumes becomes
counted, so the caller moves its unit in — passes every reproducer, fixes the
memory, and segfaults the produced compiler on almost every module.
`ssarc.caller_sigs` reads a counted parameter as "the caller moved its unit in",
which for a declared `own` position the checker's E051 guarantees and for an
inferred one nothing does; an AST-lowered caller passing a borrowed value
supplies nothing, and the produced callee releases what it was never given.
`FERN_SEM_IR_SKIP=<caller name>` reproduces it in one step. A parameter mode is
half of a contract whose other half the checker enforces on source the user
wrote, so it is not free to be inferred.

**What landed.** The AST lowering's shape — hold the receiver's retain back,
push through the non-consuming `__fern_arr_push`, retain the result on pointer
identity — plus the caller-side may-grow bracket of #4873, so a caller whose
array is still live holds a second count across the call and the callee's gate
sees the copy it owes.

That shape alone corrupts arrays, because of what the grow path does:

> `__fern_arr_push` **frees nothing, ever**. Its grow path allocates a fresh box,
> memcpy's the element POINTERS into it without retaining them, and abandons the
> old buffer. The old buffer and the new one then share one count per element.

The AST lowering settles that by never releasing the old buffer — the
"LOAD-BEARING LEAK" comment in `asm_ir.fern`, and why the AST build leaks 524 KB
on the reproducer. The semantic path's ledger balances every unit, so its caller
does release it, and the aliased elements die under the box that now points at
them.

So the grown arm counts them: `__fern_arr_inc_elems` over the RECEIVER names
exactly the boxes the fresh one aliases, since the receiver is untouched by the
push and the element just pushed is not among them. O(n) per grow, amortised
O(1) against the doubling, and the same shape `sole_owned_base` already uses on
its copy arm.

Reproducer: 2 ms with allocs and frees balanced.

**It is not what the 11.5 GB is made of.** Measured on `lexer.fern`, both
exit 0: 11,550 MB without the deferral, 10,726 MB with it. 7%. The 18 MB
#9388 reported for this same lowering came from a run that exited 139, and
the mode-inference attempt's 18 MB from one that exited 134 — a peak up to a
crash, not a figure for completed work.

The issue's reading of its own bisect does not survive reading the code
either: the `w`-`z` cone is the x86 assembler, but `asmcore.EmitState.write`
routes through a global strbuf and appends to no array. So the cone is right
and the shape inside it is still open, with the borrowed-parameter append now
ruled out as its cause.

The n-z and t-u-v rows' exit 139 is its own finding rather than a consequence
of the exclusion: an excluded body falls back to the AST lowering, so a mixed
module that faults is a boundary bug.

### The field append, admitted

The shape inside the `w`-`z` cone was the struct-FIELD append of #8785, in the
x86 GAS assembler: `x86_gas_mem_op`'s `code: i32[]` is the machine-code buffer,
and `a = X86Asm { ...a, code: a.code.append(op) }` copied it per byte, because
`a.code` is a projection the plan retains before `sole_owned_base` reads the
count. `FERN_CLIFF_REPORT=1` on a 200-function input measured it exactly:
half the copies of the AST build and 664x the bytes — 5.4 GB.

The fix is the plan admitting the site (`ssaunits.field_grow_root`), the
lowering growing it in place and moving the buffer out of the field
(`ssarc.append_field`), and both kinds of caller bracketing the fields a
produced callee may grow: a produced one from the callee's plan
(`ssaunits.grow_rows`, which is why every body is planned before any is
lowered), an AST-lowered one through the may-grow registries rerun with the
produced masks seeded in (`irlower.regrow_sigs`). The record's own count
covers what no caller names — a record stored in a container, or bound twice.

The assembler's own site is the field HANDED to a callee that appends —
`a = X86Asm { ...a, code: x86_osz(a.code, size) }` — and the same admission
lets the caller hand the buffer on without a bracket (`ssaunits.hands`), with
the rows closed transitively over every produced plan (`ssaunits.grow_table`).
Mechanism and traps: `rc-log/2026-09-15-field-append-grows-in-place-on-the-semantic-path.md`.

The same admission covers a `with` on the field — `a = X86Asm { ...a,
lab_tail: a.lab_tail.with(b, at) }` — which writes the element into the
record's own buffer when the record and the buffer are each sole-held and
nulls the field, and into a copy otherwise (`ssarc.with_field`). Without it
the assembler's label placement copied its three bucket arrays per label,
and the exact-size copy left the next append no room to grow in place:
7.3 GB of arena for the assembly of `parser.fern` where the native-built
compiler takes 68 MB.

The 40,000-push reproducer through a borrowed record: 11.1 s and 8.9 GB to
60 ms, matching the AST lowering. The compiler built through the path,
against the two compilers of the section above:

| compiler | 200 declarations: peak | copies through the cliff | `lexer.fern`: peak | copies through the cliff |
|---|---|---|---|---|
| AST-built | 75 MB | 15,645, 8.9 MB | 137 MB | 16,396, 36 MB |
| semantic, before (#9398 in) | 4,067 MB | 30,765, 5.45 GB | 10,726 MB | — |
| semantic, the append admitted | 3,681 MB | 46,049, 5.43 GB | 5,819 MB | 58,214, 9.10 GB |
| semantic, the handed field too | 5,580 MB | 25,453, 1.50 GB | 6,892 MB | 25,364, 2.93 GB |

Every row exit 0. The copies are `FERN_CLIFF_REPORT=1`'s count and bytes;
the peaks are sampled RSS. Two things the table says that a peak alone
would not:

- **The bytes copied fell 3.6x on the 200 declarations and 3.1x on
  `lexer.fern`, and the peak did not.** That
  is not a leak: under `FERN_LEAKCHECK=1` the produced compiler leaves LESS
  live at exit than the AST-built one — 22.5 MB against 67.0 MB on the 200
  declarations, 29.6 MB against 142.5 MB on `lexer.fern` — and frees 1.66 M
  of 1.90 M allocations where the AST build frees 0.38 M of 1.31 M. The
  peak is churn the size-class freelist does not recycle, the shape the
  §"What stops it being the FIXPOINT" note already named. The copies that
  remain (25,000 on either input, 1.5–2.9 GB) are the next thing to
  attribute, with the same instrument.
- **The produced compiler's ANSWER on `lexer.fern` is wrong**, and was
  before either change: the semantic build of main at `b15a016` emits the
  same 150-line divergence from the AST build, at three sites: the two
  `var b: i32 = 0 - 1;` in `match_multipunct` and the `return -1;` in
  `test_mixed`, each lowered as zero minus one, which the AST build folds
  to `movq $-1` and the produced build emits as `xorl; movq $o, %rcx; subq`.
  The constant op's `str` field is the literal's text, a view the constant
  folder sliced out of a string its frame then released, so the byte read
  back was whatever the reissued block held. The 200-declaration input's
  output is byte-identical. That was #9407, two defects: the sanitizer's abort
  was an AST-lowered caller releasing, on its rebind, an `own` array it had
  moved into a produced callee that consumed it — the boundary contract is
  `irlower.own_consumed_positions`, seeded from the produced bodies and closed
  over the AST-lowered forwarders
  (`rc-log/2026-09-15-an-own-array-consumed-across-the-mixed-boundary.md`) —
  and the wrong assembly was the view, which a produced caller now copies for
  a callee that keeps it
  (`rc-log/2026-09-15-a-lent-view-is-copied-for-a-callee-that-keeps-it.md`).

### The leaves that are left, by measured size

On the compiler compiling itself there are none: **8,307 of 8,307
declarations produce** (2026-09-16, `FERN_SEM_IR=1 FERN_SEM_IR_REPORT=1
bin/fern-selfhost -target x86-64-linux -o … examples/self_host/fern.fern`),
and the report is the tally line alone. The compiler that comes out compiles
`lexer.fern`, `parser.fern`, `checker.fern` and the whole tree **byte-identically
to the AST build**, on x86-64, arm64 and wasm. The table this section held —
44 `value-returning body falls through`, 37 `unsupported map shape`, 33
`uninstantiated generic`, and six smaller rows — was one leaf wearing nine
names.

The leaf was in the PARSER, not here. `parser.monomorphize_module` clones a
generic whose variable is bindable from its parameters (`first[T](xs: T[])`,
`append_all[T](into: T[], more: T[])`, every `astwalk` fold) into
`first__i32` with `type_params` emptied — and `type_param_count` copied from
the template. `generic_decl` reads either field, so every clone was a
template with nothing to bind: refused as `uninstantiated generic` without a
report line (a template's refusal is silent by design), and every caller of
one, transitively, as `call target was refused`. That is what kept the
`astwalk` folds and every callback through them on the AST side, and it is
the whole of #9414: `ident_of` was AST-lowered because `fold_expr_pruned__…`
read as a template. Underneath it a second bug in the same pass:
`subst_ty` did not look behind a function type's `own T` slot, so a fold's
callback parameter still spelled `own T` in the clone, which the clone-as-
template reading had been hiding. Both are one field and one branch
(`clone_bg`, `subst_ty`); the production suite's `generic-clones` program
holds the three shapes and fails at 0 of 3 without the first fix and 1 of 5
without the second.

`parser.clone_struct_method` had the same field. It clones a generic struct's
method per receiver instantiation (`OrdMap[i32, i32].insert` out of
`insert[K: cmp.Ord, V]` on `OrdMap[K, V]`) with `type_params` emptied and, until
2026-09-20, `type_param_count` copied from the method — so every method that
redeclares its receiver's variables read as a template, and every caller of
one, transitively, refused as `call target was refused: uninstantiated
generic`. That was the whole of the `uninstantiated generic` root the corpus
census counted (117 sites: the ordmap, ordset, pmap, pset and set tests and
benches). The production suite's `generic-struct-method-redeclares-receiver-
vars` and `ordmap-bounded-method-clones` rows hold it.

Measured on the 4-core x86-64 container, all three compilers built from the
same sources by `bin/fern-selfhost`:

| compiler | built by | build | on `checker.fern` | on `fern.fern` |
|---|---|---|---|---|
| native-built (`bin/fern-selfhost`) | Go | — | 4.0 s, 398 MB | 47.4 s, 4,867 MB |
| AST-lowered stage 2 | self-host, `FERN_SEM_IR=` | 59 s, 5,489 MB | 5.0 s, 903 MB | 66.1 s, 9,973 MB |
| semantically lowered stage 2 | self-host, `FERN_SEM_IR` unset | 9m26s, 8,396 MB | 7.1 s, 122 MB | 60.6 s, 830 MB |

The produced compiler's output is identical to both others' on every input in
the table. The 6,865 MB #9365 measured on `lexer.fern` is gone with the
mixing: that figure was a mixed module, and no body is mixed now. Read the
last two rows together: the compiler this path builds runs the whole tree in
**a twelfth of the memory** the AST-lowered one does, at the same speed. That
is the goal-2 gap (`make distcheck` OOM-killed at 13.9 GB, `docs/BOOTSTRAP.md`)
closed from the other side — not by porting the AST lowering's ownership
analysis, but by the lowering that replaces it.

What the substitution still costs is the BUILD: the semantic self-build ran
9.5 minutes at 8.4 GB where the AST self-build runs 59 s at 5.5 GB. callgrind
on the semantic build of `checker.fern` put 56.6% of every instruction in
`ssadeps.block_index` — the linear search for a block by id, called once per
predecessor per block pair per round of the dominator solver — and another
6% in `ssalayout.representative`, recomputed per edge per round of the
region ordering. Predecessor positions resolved once per solve and a
representatives table once per region take the self-build to **3m31s** at
the same peak; `checker.fern` goes 30 s to 12.7 s against 3.7 s for the AST
lowering, with the output byte-identical. What remains is `semsource` +
`ssaunits` + `ssarc` running once per declaration on top of the AST lowering
that still runs for the eligibility verdict.

**Production is all or nothing.** A module with a refused declaration keeps
the AST lowering whole: `semlower.substitution` reports the refusals and the
tally as `produced 0 of N declarations … K refused, the AST lowering stands`
and hands the emit `no_sub()`. A produced body beside an AST-lowered one is
two memory conventions on one module — every crash this path has had was a
mixed module — and the contracts `prune` reads cover the crossings it can
see, not every data structure that crosses. A TEMPLATE's row is no body of
the module's — its produced instances stand for it, and one nothing
instantiates is called by nobody — so a template with produced instances,
or an uninstantiated one, is accounted as kept without a body; only a
template with a refused instance is a refusal, reported under the
template's name. The bisect knobs
(`FERN_SEM_IR_ONLY` / `FERN_SEM_IR_SKIP`) keep the mixed module, which is
what they exist to halve, and the production suite's skip legs run under
them. The other half of the same decision: a declaration the substitution
produced is not lowered by the AST lowering at all (`ircore.lower_gated`
takes the substitution and reads the produced body in its place), so the
AST lowering's verdict is not asked for it, and a module the semantic
lowering produces whole compiles where the AST lowering would have refused
it — a `match` on a bare `Some(x)`, an empty array literal as a match arm's
value — and the semantic self-build no longer pays for two lowerings of
every declaration.

**The fixpoint holds.** The produced compiler rebuilding the whole tree
through the semantic path emits assembly byte-identical to the native-built
compiler's, at a lower arena high-water mark: 4.69 GB against 7.21 GB
(4.4 GB against 6.5 GB peak RSS), in 4m34s against 2m47s. What exhausted
the 16 GiB arena before (19 minutes in, then 10) was the AST lowering
running inside the produced compiler for every one of the 8,322
declarations it was also producing: not a leak (a `FERN_LEAKCHECK` build
of the produced compiler frees everything it allocates on `checker.fern`
with the flag on) but the AST lowering's own working set, doubled by
being asked for a verdict the substitution then discarded. With
`ircore.lower_gated` reading the produced body in the declaration's place
the AST lowering runs for nothing on a whole module, and the phase readout
puts the produced compiler's arena at 0.31 GB after the gate, 0.83 GB with
the bodies built, 1.36 GB lowered and pruned, 4.13 GB with the registries
rewritten and the helpers merged, 4.69 GB emitted; the native-built
compiler's at 1.77, 3.09, 3.66, 5.75 and 7.21 GB at the same points. The
registry rewrite is the next lead in both. `TestSelfHostSemanticWholeCompilerX86_64`
pins the tally, the byte-identity and the fixpoint.

**The binary fixpoint holds too.** The produced compiler compiling the
whole tree to an ELF binary (`-o`, its own assembler and linker in
process) reproduces itself byte for byte. Three sites in the assembler
stood in the way, each a buffer lent where the lowering needed it owned:
the code buffer projected out of `X86Asm` and lent to the byte emitters,
the label tables written through `with` on a field with no in-place path,
and the .eh_frame renderer's buffer appended to through a borrowed
parameter more than once. With the first two fixed the self-rebuild took
6m15s at 11.9 GB peak RSS; `rc-log/2026-09-16-a-record-lent-on-through-a-wrapper-grows-under-its-own-count.md`
has each site's measurement; with all three fixed it takes 4m47s at
6.06 GB.


Read these the way this file reads every leaf: probe the refused functions by
name before building, because the histogram has repeatedly ranked the work
wrong. The tuple slice above is not the first time a leaf that read as a
missing analysis turned out to be one construction site choosing the narrower
of two ops that both already existed — the array element's own width and
`op_call_indirect_sig` are the same shape.

### The conformance corpus's leaves, 2026-09-22 (#9550)

The corpus census — the first `FERN_SEM_IR:` line per case over
`conformance/cases/*/`, x86-64 — read **526 of 599 cases producing whole**
after this round, from 512. Of the 73 that do not, 70 are diagnostic
fixtures with an `expected.error` that never reach the lowering, so three
real cases are left. Seven leaves closed here, each a small rule the
semantic path had never stated rather than an analysis it lacked, and each
is a `TestSelfHostSemanticProduction` row; two more closed on main the same
day (`lambda_tuple_return` and `defer_block_form`, the rc-log's `f` and `g`
entries):

| case | first refusal | what was missing |
|---|---|---|
| `use_callback_bind`, `arrow_lambda_block_body` | `call target has no semantic contract: main$wrap0` | the `use` callback's parameter had no type; `checker.pretype_module` stamps it from the callee's callback slot ahead of both checking and annotation (`docs/SELFHOST-CHECKER-PORT.md`, same date) |
| `map_narrow_int_keys` | `aliased column element: u8` | `ssasem.retained_column` admitted only an `i32` scalar column; a `u8`, `u32` or `boolean` column bit-copies the same i32-shaped cells |
| `trailing_commas` | `call arity` | explicit type arguments at a call are erased by the parser and only counted; the count was refused on all three call paths, where the inference from arguments and destination already decides |
| `labeled_loops` | `loop exit names no enclosing loop` | `parser.desugar_ranges_one` built the counting while without the source label |
| `char_byte_literals` | `unsupported literal width` | `literal_type` knew no `char` suffix, and the verifier's integer-constant rule excluded a char |
| `op_overload_nested` | `operator contract: V + V` | only the comparisons dispatched through a nominal operand's method; the five arithmetic operators and unary minus now take `add` … `rem`, `neg` the same way |

The three left are design boundaries this file already names rather than
leaves of the same kind: a `str` result escaping its source
(`alloc_flat_method_identity_return`), a view lent past its frame
(`string_slice_option`), and a `dyn Trait` call (`dyn_trait_dispatch`). A
fn-typed local holding a function whose parameter is itself callable
(`var t = taker; t(lambda)`) produced nothing when this section was written
(`function signature slot`); it produces since #10024, which admits a
function type nested in a function value's signature.

## Retiring the AST lowering

The typed path is the default, and a module it does not produce whole falls
back to the AST lowering (`irlower`) with nothing to say so.
`FERN_SEM_IR_STRICT=1` makes that fallback a hard error instead. The refusals
are printed as `FERN_SEM_IR_REPORT` would print them, and the compile exits 3.
It is the measurement the retirement waits on: the AST lowering can go when
nothing needs it.

What the typed path produced whole, 2026-09-24:

| input | result |
|---|---|
| `conformance/cases/` | all 540 cases that compile, once a variant literal settles an unsuffixed float payload (`f64_tryop_widen`'s `Some(3.14)?`). `diag_e068` expects a lowering error. |
| the compiler compiling itself | every declaration |
| `examples/` outside the compiler | every program, once `word_freq` copies what it stores (`rc-log/2026-09-24-g-…`) |
| `coreutils/` | all 106 programs |
| e2eselfhost under strict, shards 0–1 of 12 | 438 of 440 tests |

`TestSelfHostOverReleaseReportArm64`'s `__rc_dec` produces now
(`rc-log/2026-09-24-i-…`). `TestSelfHostStrEqSymbolTypeChecks` still refuses,
on `__fern_str_eq` rather than on the `__raw_data` beside it.

### The runtime helpers never reach the typed path

The Fern-source runtime functions (#2649, `asmcore.rt_src_*`: file open,
writes, sockets, process control, the map finder and more) are appended to a
program on demand. `asm_ir`, `asm_arm64_ir` and `wasm_ir` compile them through
`emit_ir_runtime_fern_fn`, which calls the AST lowering directly and never
asks the substitution. They are written on the raw floor: `__raw_alloc`,
`__raw_store8` / `__raw_load8`, `__raw_store_ptr` / `__raw_load_ptr`,
`__raw_string`, `__raw_data`, `__raw_array`, `__raw_arr_box`, `__raw_addr`,
`__raw_scratch`, `__raw_environ`, `__raw_splice_pipe`, `__syscall3`–`6`,
`__fern_map_find` and `__fern_str_eq`.

The raw floor is typed and lowered on the typed path: one table,
`checker.raw_floor_sigs`, gives the checker its signatures and `semsource` its
contracts, and `ssarc.raw_floor_ops` emits the op `irlower` emits for each.
An address is a `usize`, an offset, byte or length an `i32`, and a syscall's
operands and result are `i64` words. `__raw_string` and `__raw_array` hand a
block to a fresh `string` or `i32[]` the caller owns; `__raw_data` is lent its
string. `TestSelfHostRawFloorIsTypedWhole` fails when `irlower` lowers a
raw-floor name either table lacks.

The x86-64 and arm64 backends ask for a helper's typed lowering first:
`emit_ir_runtime_fern_fn` calls `EmitState.rt_lower`, which the CLI sets to
`semlower.runtime_bodies` through `ircore.Sub`, so no backend links the
pipeline. A source that does not type-check, or that the typed path does not
produce whole, keeps the AST lowering. `FERN_SEM_IR_REPORT` prints
`runtime <name>: produced` for each helper it took. Wasm compiles no
Fern-source helper; it serves them as hand-written WAT.

What is left, in order:

1. The helper sources are rewritten against those types, which is what routes
   each one. `chr`, `str_concat` and the four integer `to_string` helpers are
   done. The rest were never checked, so they hold every address as an `i32`
   and pass `i32` words to the syscalls; each needs its locals retyped and its
   syscall operands cast. The ones that build an `IoError`, `FileStat` or
   `ProcessResult` also need the builtin declarations in the module they are
   checked in. `__fern_map_find` and `__fern_map_delete_rel` call through a
   bare code address (`eqfn(k, key)`), which has no typed spelling yet.
   `__fern_str_eq` takes either a string or a raw pointer today, and needs one
   signature.
2. Strict mode goes green over every suite. The fallback then becomes the
   error, and `FERN_SEM_IR=` loses its off column.
3. The AST lowering is deleted, along with the differential legs that compare
   against it.
