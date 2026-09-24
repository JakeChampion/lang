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

A `defer` / `errdefer` action has no enclosing function of its own to propagate
out of — it runs on the exit edges of the function that registered it, including
the failure edge `?` itself takes — so `?` directly inside one is refused
(`E079`). The rule follows the exits, not the syntax: a `defer` nested in a
lambda body registers on the LAMBDA's exits, so a `?` in its action is the same
refusal one level down.

A `?` inside a lambda that is merely part of the action is a different thing —
it leaves the lambda, not the function whose defer replays it — so `E079` does
not fire there. `?` in a lambda propagates to the lambda's own return type, the
annotated one or, for an unannotated lambda, the one its returns infer, with the
failure edge counted among them.

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

The self-host parses `@try` and carries it on `EnumDecl.try_marker`, enforces
the same E078 shape rule, and lowers `?` on a marked enum through the IR path on
all three backends.

Where native reads `checker.Info.TryShapes`, the self-host has no checker `Info`
at lowering time, so `irlower.try_enum_shape` re-derives the same answer from
the `StructTab`: exactly two variants on the `ehead`/`enext` chain (which runs in
declaration order, so "variant 0" is meaningful), the first carrying one `__ev`
payload, the second zero or one. Admission is therefore by **shape, not by
marker** — native has already enforced the opt-in, and the self-host compiles
native-valid programs.

The generated code differs from the Option/Result path because the value
representation does. An Option / Result box is `[tag@0, payload@8]`, so its
discriminant is an integer compare and its payload an offset-8 read
(`opt_tag` / `opt_payload`). A marked enum's box is an ordinary variant box
carrying its shape at offset 0, so `?` lowers to the ops a `match` arm already
uses on the same value: `variant_is` for the discriminant, `struct_get` at the
field's own width for the payload, and `struct_make` with zero fields to build a
payloadless failure. Reading the payload the same way `match` does is what makes
an erased generic (`Maybe[T]` at two instantiations in one module) and an f32
payload come out right.

There is one gap left, and it is in the self-host CHECKER, not the lowering: its
E042 subset only flags a `?` whose operand is a known scalar primitive, so `?`
on an UNMARKED user enum is accepted where native reports E042. That
conservatism predates the marker and covers every non-primitive operand — see
#9331.

## Diagnostics

| Code | Meaning |
|---|---|
| `E078` | an `@try` enum does not have the shape `?` requires |
| `E042` | `?` on a type that is not `?`-able, outside a function, or with a mismatched enclosing return type |
| `E079` | `?` inside a `defer` / `errdefer` action |
