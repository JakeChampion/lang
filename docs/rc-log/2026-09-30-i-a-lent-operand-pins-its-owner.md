# 2026-09-30 — a call's lent operand pins what it borrows from (#10832)

Self-host (`ssaunits.plan`, `ssaunits.verify`).

A call that is the last use of a value the frame owns, and that also lends the
callee a borrow of that value, moved the value's unit into the call:

```fern
var recv: Ty = make(k, v);
match (recv) {
  TM(mt) => { return columns(done, mt, recv); },
  _ => {}
}
```

`columns` returns `recv` on one path, so the inferred modes
(`2026-09-21-a-consumed-parameter-is-counted`) count that parameter. On the
other path the callee releases `recv` at entry, which frees `mt` and its
fields, and then reads `mt.key`. The planner treated `recv` as dead at the call.
It joins the dead set through `mt`'s dependency, so `choose_ids` moved its unit
into the counted slot.

The checker has this shape in `check_call_expr` → `literal_insert_columns`. A
stage 1 the typed lowering built read a freed `Map` type when typing
`if (false) { Map { a: 1 } } else { Map { id(a): 2 } }`. The block had been
reused, first for `ret_typevar_arg_type`'s `(Type, boolean)` tuple and then for
E031's message text, so `-check` segfaulted in `type_label`.

## The fix

`lent_owners` names what a direct or indirect call's uncounted operands borrow
from. `choose_ids` retains those values into their counted slot instead of
moving them, and they drop after the call. A builtin operation is unaffected:
it acquires its counted operands before it releases anything, but a callee can
release a consumed parameter at any point.

The replay now rejects the old plan: "a lent call operand borrows from a unit
moved into the call". Before this, `read_error` checked every operand against
the state *before* the step's moves, so a move of the owner passed.

## Measured (x86-64)

| program | before | after |
|---|---|---|
| `TestSelfHostLentOwnerCall` source, default | exit 1 | 42 |
| same, `FERN_RC_FREE_DEBUG=1` | 124 (use after free) | 42 |
| same, `FERN_LEAKCHECK=1` | exit 1 at the first check, 7 / 7 | 42, 23 / 23 |
| `fern -check` on the issue's program, stage 2 | SIGSEGV | exit 0 |

The census balances either way: the moved unit is released once, just too
early.

The whole compiler is 640 bytes larger (12,785,464 to 12,786,104, +0.005%)
when the same sources are built by a stage 1 with and without the fix.

## Traps

- **Bisecting with `FERN_SEM_IR_SKIP` cannot find this class.** Any skip makes
  the module mixed, and a mixed module keeps its declared modes (rung 3 of the
  inferred-modes entry). Without an inferred counted parameter there is no
  consuming callee, so every bisection step read as clean.
- **The quarantine did not catch the checker crash.** `type_label` reads a
  freed Type's fields without touching its rc word, and nothing was recycled,
  so the stale read returned the right answer. The reproducer's quarantine leg
  fires only because the callee retains a field of the freed block.
- What found it was gdb on the crashing stage 1, reading the `types` array
  handed to `mx_e031`. The first argument is in `rax`, an array is its length
  and then its elements, and a union value's first word is its tag. The second
  arm's `Map` held a pointer to a block whose first word was a Type pointer,
  not a tag. That was the tuple.

## Tests

- `TestSelfHostLentOwnerCallIR{X86_64,Arm64,Wasm}`: a match payload and a
  record field, each lent beside their owner. There are census legs on x86-64
  and wasm, and a quarantine leg on x86-64.
- `TestSelfHostSSAUnits`: `call-lent-operand-owner` pins the retain and the
  drop, and `call-moves-lent-owner` pins the replay's rejection of the move.
- `TestSelfHostCLIStage2X86_64/check-map-literal-branches`: the issue's program
  under `-check` by a stage 2 the fixed stage 1 built.
