# `?` and the `@try` marker

`?` is not tied to `Option` and `Result`. Any enum marked `@try` can be used
with it:

```fern
@try
enum MyOpt[T] { Here(T), Gone }

function pick(m: MyOpt[i32]): MyOpt[i32] {
    var v: i32 = m?;     // Gone → early-return Gone
    return Here(v + 1);
}
```

## The shape obligation

`@try` is an opt-in, not an inference — a two-variant enum is not silently
short-circuitable. It carries a shape requirement the checker enforces at the
declaration (**E078**):

- exactly two variants;
- variant 0 (the success variant) carries exactly one payload;
- variant 1 (the failure variant) carries zero or one.

That is not a rule invented for the marker. It is what every backend's `?`
lowering already assumed: success at tag 0, failure at tag 1, payload at the
offset `payloadLayout` gives. The marker makes an existing structural
requirement checkable, at the one place the author can act on it.

`Option` and `Result` are `?`-able without the marker — they are builtins and
predate it — but they are recorded the same way and satisfy the same shape.

## The two lowerings, named by shape

`ast.TryKind` selects how the early-return value is produced, and follows from
the failure variant's shape rather than from which enum it is:

| Kind | When | Lowering |
|---|---|---|
| `TryKindBuild` | failure variant is payloadless | build a fresh tag-1 value of the enclosing function's return enum |
| `TryKindForward` | failure variant carries a payload | the source value is already the answer — forward it unchanged |

`Option` takes the first, `Result` the second, and a marked enum takes whichever
its shape dictates.

## What `?` requires of the enclosing function

The failure value propagates *out*, so the enclosing function must return **the
same enum**. Cross-enum propagation — `MyOpt?` inside a `Result`-returning
function — is what Rust needs `FromResidual` for, and `@try` deliberately does
not have it. Adding it later is additive.

When the failure variant carries a payload, the source's and the return's must
match, or be convertible by one of the two existing hooks: a `dyn`-trait box
(#3234) or a `from(E1): E2` constructor (#2674).

## One predicate, on purpose

Everything that asks "is this `?`-able, and what does it unwrap to" goes through
a single place per compiler:

- native: `checker.Info.TryShapes`, read by the checker's `*ast.TryOp` arm, the
  IR's errdefer gate (`isTryReturnType`), and — via the enum decl's marker — the
  interpreter's `isErrReturnValue`;
- the interpreter's `?` itself needs no lookup at all: it branches on the tag,
  as every compiled backend always has.

That is deliberate. If `@try` is ever replaced by a `Try` trait, only the
population of that map changes; the IR, the interpreter, errdefer and the
backends never learn which it was. The marker was chosen over a trait because a
trait name must be resolvable without an import and Fern has no prelude — see
#9302 and #9322 — and because the shape obligation above is structural anyway.

## The self-host

The self-host parses `@try` and carries it on `EnumDecl.try_marker`, so a marked
program round-trips and reports no diagnostic. Its `?` **lowering** is still
Option/Result-only: a marked enum reaching `lower_try` bails, which under
`FERN_STRICT_IR=1` names the bail site (`did not lower: unary \`try_\``) and
emits nothing. That is a capability gap, not a miscompile — generalising
`lower_try`, `try_opt_type` and the `TyOption` / `TyResult` constructors in
`asmcore.fern` is the next slice.

## Diagnostics

| Code | Meaning |
|---|---|
| `E078` | an `@try` enum does not have the shape `?` requires |
| `E042` | `?` on a type that is not `?`-able, outside a function, or with a mismatched enclosing return type |
