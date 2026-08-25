# An enum read through a match EXPRESSION reclaimed nothing — killer-drops slice 10

`var v: E = E.A([i, i + 1]); consume(v);` over 100 rounds: **200 allocs / 0
frees** against native's 200/200. Not one block — the enum box and its payload,
every round. The identical read written as a match STATEMENT was already flat.

## How it was found, and what it is not

The lead was the construction-retain matrix's `enum__param` cell. That cell is
102/100 against native's 102/102 — exactly two blocks, leaked ONCE at `main`'s
exit rather than per round, because its `keep` is built once and passed into a
loop. The credit dump showed the caller getting no credit row at all, which
pointed at the enum escape walk's call-argument refusal.

Bisecting the general shape moved it somewhere better:

| shape | before | native |
|---|---|---|
| bound, never read | 200/200 | 200/200 |
| read by a match STATEMENT | 200/200 | 200/200 |
| read by a match EXPRESSION | **200/0** | 200/200 |
| passed to a helper that reads it by a match STATEMENT | 200/200 | 200/200 |
| passed to a helper that reads it by a match EXPRESSION | **200/0** | 200/200 |
| the same, with the match expression wrapped in `+` | **200/0** | 200/200 |
| a value block that does not mention the name | 200/200 | 200/200 |

The bind shape is irrelevant, and so is the call argument per se. What decides
it is whether the name is MATCHED inside a value block — in the caller or in the
callee.

## Mechanism

Slice 6 taught `expr_unsafe_for`'s ExprLambda arm that a value block
(`match`/`if`/block/comprehension expression, desugared to an inlined zero-arg
IIFE) is not a closure, and recursed with `body_unsafe_for` — the STRICT walker.
That was right for its own caller and wrong for the two walkers that carry the
match-borrow reading, both of which delegate expressions to `expr_unsafe_for`:

- **`stmt_unsafe_for_match_borrow`**, which feeds `borrowable_params_of` /
  `borrowable_params_interproc`. It reads a bare-ident match scrutinee as a
  borrow; the strict walker does not. So a callee that reads its param through a
  match expression had that param marked non-borrowable — and every CALLER then
  refused its own enum local's release. This is the expensive half.
- **`ef_unsafe_expr`** (slice 5's enum-field fork), which is the caller's own
  `var t = match (v) { … }` read.

## The fix, and why it is not one line

Making the value block borrow-aware for everyone is a one-line change and it
does fix both shapes. It is also wrong: `precise_drop_names` reads
`body_unsafe_for` and widens to the match-borrow reading for `is_rcopt || is_opt`
only — "so this widens exactly one class at a time", says the code. The
one-liner silently widened the enum class too, and the plan then claimed
`preciseDrops: 1=x` on a box its own `consumingMatchReuse` still owned. Runtime
was unchanged on that shape, but a plan that says free-early while the emitter
declines is a trap armed for whoever makes the emitter honour it.

So the mode is carried explicitly. `expr_unsafe_for` stays the strict entry
point and becomes a wrapper over `expr_unsafe_for_vb(..., vb_borrow)`; the
statement/body walkers gain `*_vb` siblings the same way (`_alias` was already
threaded through them, so the shape existed); and the two match-borrow walkers
pass `true`. The strict walker recurses strictly, exactly as before.

Threading three walkers was not enough, and the shortfall is worth recording
because it is the same mistake one level down. `+` and the comparisons do not
recurse through the ordinary operand path — they hand their operands to
`binop_operand_unsafe_for`, which goes to `expr_unsafe_for_view_pos`, which
falls back to the STRICT `expr_unsafe_for`. So a helper written
`return (match (e) { … }) + k;` stayed refused while the same helper written
`return match (e) { … };` was fixed. Both of those helpers take the mode now.

`ef_unsafe_expr` passes `true` as well, so a value block reached from the
enum-field fork gets the match-borrow reading — but NOT the enum-field
forgiveness, which does not ride into the block. That direction is conservative:
a field share written inside a value block keeps the old refusal rather than
gaining a credit nothing proved.

## Results

Six shapes at native parity (200/200, from 200/0 on the three that were broken),
pinned across x86 / arm64 / wasm by
`internal/e2eselfhost/self_host_enum_value_block_borrow_test.go`.

The negative control is the row that matters: a value block whose VALUE is the
enum (`var y: E = if (c) { v } else { mkv(i) }`) stays refused at 300/0,
unchanged by this slice — the strict walker's StmtReturn arm catches a name that
really does leave the block, inside one exactly as outside one.

## A pinned test was recording the leak as a design decision

`call_arg_escape_refused` in `self_host_enum_field_share_test.go` pinned 300
allocs / 100 frees and said in its comment: "REFUSED, and still refused: the
source escapes through a call argument, which no retain covers. It leaks (the
safe direction)."

Native is 300/300 on that exact program. The refusal was never correct — `sink`
only READS its param, through a match expression wrapped in `+`, so the param is
borrowable and the source keeps its claim. What refused it was the operand path
above. The row is renamed `call_arg_borrowed_by_callee` and re-pinned at
300/300.

Worth stating plainly: a want that encodes the current behaviour and a comment
that explains why the current behaviour is right will survive any number of
green runs. This one did, across two slices that both cited it.

## Still open

The `enum__param` matrix cell, 102/100 against native's 102/102. Its `keep` is
built once in `main` and passed into a loop, so the two stranded blocks are a
one-off at exit rather than per round — a different admission from this one, and
still uncredited.
