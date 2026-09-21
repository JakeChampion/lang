# 2026-09-21 — a composite compares through its own method

`conformance/cases/derive_multi_payload` produced 0 of 140 declarations on one
refusal: `operator contract: E == E`. Two smaller cases, `eq_struct_enum` and
`ord_struct_enum`, produced none of theirs for the same reason.

`ssasem.binary_result` answers a type for integers, floats, booleans,
codepoints and text, and `typeinfo.unchecked()` for everything else — so a
struct or an enum on either side of `==` reached the refusal. That is the right
answer for the operator: a struct is a BOX, and comparing two of them as
integers compares their addresses.

## The fix

A composite comparison is structural, through the method the type's `@derive`
or its own `impl` supplies: `a == b` is `a.eq(b)`, `a != b` is `!a.eq(b)`, and
an ordering is `a.cmp(b) <op> 0` against the -1/0/1 `cmp` answers. That is the
desugar the native checker applies (`EqCall` / `CmpCall`) and the one
`irlower.lower_binary` repeats for the AST leg; the typed path had neither.

`binary` now produces its LEFT operand, reads the verb off that operand's
produced type, and — when the type names one and a contract for it exists —
hands the still-written right operand to `invoke_method`. Taking the decision
between the two operands is what keeps each produced once: the method call
produces the right one, and producing it here as well would give the call two
values for one expression.

A nominal type with no such method falls through to the operator refusal,
which names the type rather than a contract the program never asked for.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle.

| program | before | after | typed held |
|---|---|---|---|
| a struct and a three-shape enum over all six operators | 0 of 53 | 53 of 53 | 0 B |
| `conformance/cases/derive_multi_payload` | 0 of 140 | 140 of 140 | 0 B |

Each answers what native answers, on x86-64 and arm64, and the two lowerings
agree.

Corpus census (865 seeds), both legs run here with the same script:

| | before | after |
|---|---|---|
| programs produced whole | 828 | 831 |
| declarations produced | 84,600 of 87,231 | 84,750 of 87,234 |

The three that become whole are `derive_multi_payload`, `eq_struct_enum` and
`ord_struct_enum` — every program the leaf named. The compiler's own seed gains
the three new declarations and still produces whole.

## Traps

- **The cost is on every comparison, so it cannot be a type query.** The first
  version asked `checker.expr_type` for both operands before producing either,
  which is a full recursive re-check of two expression trees on every `i < n`
  in the program. Reading the verb off the left operand's PRODUCED type costs
  one type-union match instead, and needs no second pass.
- **Deciding after both operands are produced is too late.** The natural place
  looks like the existing refusal — by then both types are in hand. But the
  rewrite calls a method whose ARGUMENT is the right operand, and the call
  produces its own arguments, so a right operand already produced would be
  produced a second time. The operand is an arbitrary expression: `f(x) == g(y)`
  would call `g` twice, and the first result's box would be built and then left
  for the planner to drop.
- **A generic struct reaches this already concrete.** `struct_name` answers ""
  for a `TypeStruct` carrying type arguments, which reads like `Box[i32] == …`
  falling through to the operator refusal. It does not: `monomorphize_structs`
  clones the instantiation into a nullary `Box__i32` before this boundary sees
  it, so the bare-name lookup is what every reachable comparison needs. Both a
  direct `Box[i32] == Box[i32]` and one inside a generic `same[T](a: Box[T],
  b: Box[T])` produce whole and answer what native answers.
