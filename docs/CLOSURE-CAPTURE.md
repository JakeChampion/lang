# Closure-capture semantics — the scalar/reference asymmetry

Date: 2026-07-06, revised 2026-07-20. Status: shipped and enforced
(E049); this document writes down the contract both compilers must honour
identically.

**2026-07-20 correction.** This document originally described capture as
by-VALUE for scalars — a snapshot taken at closure creation. That was the
pre-#2896 model and has not been the implemented behaviour for some time:
#2896 (scalars) and #5301 (pointers) deliberately moved every compiled
backend to by-REFERENCE to match the interpreter, which defines the
semantics. Verified unanimous across `-interp`, native x86-64, the
self-host x86-64 and arm64 backends (both AST and IR paths), and the
self-host wasm IR path. The E049 half of this document was always accurate
and is unchanged; only the by-value half was stale (#5479).

## The rule in one sentence

A closure captures the variables it reads from an enclosing scope **by
reference** — outer and closure share one cell, so a mutation on either
side is visible to the other — while whether writing the captured name
back is *allowed* still depends on whether the captured type is a scalar
or a reference.

## The two cases

**Scalar captures** — `i32`, `i64`, the unsigned widths (`u8`, `u32`,
`u64`, `usize`), `f32`, `f64`, and `boolean` — are **shared**, not copied:

- mutating the capture inside the closure is legal (the stateful
  "counter closure" works), and
- that mutation **is** visible in the enclosing scope, and a later change
  to the outer variable **is** visible inside the closure.

```
function make_counter(): () => i32 {
    var n: i32 = 0;
    return function (): i32 { n = n + 1; return n; };   // OK — shared cell
}

function main(): i32 {
    var n: i32 = 1;
    var f = function (): i32 { return n; };
    n = 99;
    return f();          // 99 — one cell, so the outer write is visible
}
```

The mechanism is `closureconv.BoxMutatedCaptures`: a local that is
captured by some closure AND assigned anywhere in the function — inside
the closure or in an enclosing scope — is rewritten into a 1-element heap
cell that both sides share. Gating the box on assignment *anywhere*
(rather than only inside the closure) is what makes capture cohesively
by-reference and symmetric in both directions.

**Reference captures** — `string`, arrays, `struct`, `enum`, tuples,
maps, and trait objects (`dyn`) — share the underlying buffer with the
outer variable (capture copies the *pointer*, not the pointee). So:

- reading the reference, or mutating *through* it where the type allows,
  observes the same heap value the outer scope sees, but
- **reassigning the captured name inside the closure is `E049`** — it
  would not take effect in the enclosing scope (the outer variable still
  points at the old value), and it closes one reference-cycle vector: a
  closure whose environment holds a pointer could be made to point back
  at a value that points at the closure, reconstructing a cycle that the
  reference-counting runtime cannot collect.

```
function f(): i32 {
    var s: string = "hi";
    var g = function (): i32 { s = "bye"; return 0; };   // E049
    return g();
}
```

The fix for a reference capture is to **return the new value from the
closure** instead of writing it back.

### The enclosing scope's store is `E049` too

The capture is shared, so the *enclosing* scope writes the same box, and what
it stores is checked there too (#8440). `g = f` where `f` captures `g` would
close `box -> closure -> env -> box`:

```
function main(): i32 {
    var g: () => i32 = (): i32 => { return 1; };
    var f: () => i32 = (): i32 => { return g(); };   // f's env holds g's box
    g = f;                                           // error[E049]
    return 0;
}
```

The rule is a TYPE-REACHABILITY test on the stored value, not on the variable:
a store is refused when its value's type can transitively hold a function —
directly, through an array / slice / tuple element, a struct field, an enum
payload, a `Map` value, or opaquely behind a `dyn`, a type parameter or an
associated-type projection. A value whose type cannot name a function can never
be the closure, so the by-reference rebinds this document exists to describe
stay legal:

```
var s: string = "a";
var f = (): i32 => { return s.len(); };
s = "bb";                    // OK — a string reaches no closure
```

One right-hand side is read by value flow instead: a closure **literal**, whose
captures the checker has just computed. A literal that captures neither the
target nor anything whose type could hold a closure has no edge back to the
cell, so swapping a captured callback on a flag stays legal:

```
var g = (): (string, i32) => { return ("abcd", 4); };
if (flip) { g = (): (string, i32) => { return ("z", 1); }; }   // OK
var h = () => g().1 + 38;
```

Every other spelling — an identifier, a call result, a container holding one —
is judged by its type alone, which over-approximates in the safe direction.

Measured against every `.fern` source in the repository — `examples/` (the
self-host compiler included), the stdlib, `conformance/` and `coreutils/`,
1128 files — and against the 11,810 inline Fern programs in the Go test
sources, the rule refuses nothing that was accepted before. The blunter rule
(refuse EVERY store into a boxed pointer capture) refuses 28 of those 1128,
`examples/self_host/fern.fern` among them, so it would break the bootstrap.

Only a `var`-declared local gets a box, so only a `var` can close a cycle. A
captured **parameter**, a `let (a, b) = …` destructuring binding and a
match-arm binding are captured BY VALUE (`collectBoxedCaptures` requires a
`var` declaration), so rebinding one leaves the closure reading the value it
snapshotted — confirmed by running each: after `g = f` the call `g()` returns
the original closure's answer instead of diverging. They are not cycle vectors
and the rule does not touch them.

## The classification is `ast.IsPointerType`

The authoritative scalar/reference split is native's
`ast.IsPointerType` (`internal/ast`): it returns `true` for `string`,
array, slice, tuple, `struct`, `enum`, function, and `dyn`-trait types,
and `false` for everything else — which is exactly the scalar set above.
Note that every numeric width, including the unsigned ones, is a scalar;
`u8`/`u32`/`u64`/`usize` are **not** references and are freely
reassignable when captured.

## Cross-compiler parity

Both the native (Go) checker and the self-host (`examples/self_host/checker.fern`)
checker must emit `E049` on exactly the same captures. This is pinned by
the checker-codes differential (`internal/e2eselfhost/self_host_checker_codes_test.go`,
the `cap-assign-*` cases), which runs both checkers on each program and
asserts identical diagnostic-code sets, and by the native checker's own
`E049` cases (`internal/checker/checker_test.go`). The self-host
enforcement lives in `checker.fern`'s `e049_*` pass (`e049_is_ref` is the
type-name classifier that mirrors `ast.IsPointerType`).

### Known self-host limitation

The self-host `E049` pass is a lightweight standalone walk that does not
thread the full type environment, so it infers an **unannotated**
capture's type only from an obvious pointer-shaped *literal* init
(`var s = "x"`, `= [..]`, `= P {..}`, `= (..)`). An unannotated var bound
to a pointer-shaped **non-literal** init — a call or another identifier,
e.g. `var s = mk();` where `mk` returns `string` — is conservatively
treated as scalar, so a write-back capture of it is **not** flagged even
though native (with full inference) flags it. This is a soundness-safe
under-approximation (it never over-flags), and closing it needs the
standalone pass to gain init-expression type inference — tracked
separately, not a correctness hazard for the common annotated / literal
forms.

The enclosing-scope half of `E049` (#8440) is native-only for the same
reason: deciding it needs the captured variable's declaration identity and
the stored value's resolved type, neither of which the standalone walk
threads. A self-host build therefore still ACCEPTS an outer rebind that
closes a cycle, and leaks it. Native-only surface, so it is debt under
#4451 rather than a free win; the checker-codes differential is unaffected
because no `cap-assign-*` case rebinds from the enclosing scope.

## Related

- `E048` (field immutability) and `E056` (array-element immutability)
  are the other two halves of the immutable-data-structures surface;
  together with `E049` they make reference cycles unconstructible, which
  is what the collector-free RC runtime rests on
  (`docs/IMMUTABILITY-MIGRATION-PLAN.md`). With fields, elements and
  `Cell[T]` payloads all closed, the capture box is the only mutable heap
  slot left, so both halves of `E049` are what carries the invariant.
- `E057` is the sibling rule for `Cell[T]` payloads (a cell over a
  reference type could likewise reconstruct a cycle).
- The write-only-scalar-capture *miscompile* is tracked separately as
  `#2850` / SH-057; this document specifies the *intended* rule, which is
  unrelated to that bug.
