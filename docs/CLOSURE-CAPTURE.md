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
  points at the old value), and, more importantly, it is the last
  reference-cycle vector the immutable-data design closes: a closure
  whose environment holds a pointer could be made to point back at a
  value that points at the closure, reconstructing a cycle that the
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

## Related

- `E048` (field immutability) and `E056` (array-element immutability)
  are the other two halves of the immutable-data-structures surface;
  together with `E049` they make reference cycles unconstructible, which
  is what lets the RC runtime stay collector-free
  (`docs/IMMUTABILITY-MIGRATION-PLAN.md`).
- `E057` is the sibling rule for `Cell[T]` payloads (a cell over a
  reference type could likewise reconstruct a cycle).
- The write-only-scalar-capture *miscompile* is tracked separately as
  `#2850` / SH-057; this document specifies the *intended* rule, which is
  unrelated to that bug.
