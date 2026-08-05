# Self-host literal settling — scoping note

**Status:** scoping only. No code. This note sizes the work and names the
gates; it does not propose a schedule.
**Motivation:** `docs/TYPED-IR-REWRITE.md` §"A carrier is only as good as the
checker behind it" — the typed-IR migration's ceiling is the checker's
precision, and this is the first place that ceiling is measurably binding.

## The defect, precisely

The self-host checker types **every** unsuffixed integer literal as `i32`:

```fern
parser.ExprNumber(n) => {
    if (n.is_float) { return t_float(); }
    return t_i32();
},
```

No magnitude test, no context sensitivity. Consequences observed:

- `var v: i64 = (if (c) { [7000000000, 9000000000] } else { … })[1]` bails the
  IR path, while the interpreter — the semantic oracle — evaluates it to the
  right answer. The `ExprIndex.ty` carrier stamps `i32` for that read, which is
  not a hole the carrier can fill; it is a wrong answer the carrier faithfully
  transmits.
- The bail is the *lucky* outcome. It only happens because
  `irlower.ix_type_tag` consults its structural walk first, so a wrong `i32`
  matches neither the f64 nor the i64 branch. A tag-first leaf would have
  lowered a truncating 4-byte read instead.

## What native does

Native does not type literals by magnitude either. It does something better and
larger: an unsuffixed integer literal is **polymorphic**, and its width is
settled from context.

- `internal/parser/parser.go` — the suffix switch leaves `NumberLit.Width == 0`
  for an unsuffixed literal. Only `42i64` / `7u8` / … pin a width at parse time.
- `internal/checker/checker.go`:
  - `settleNumeric(e, hint)` — the entry point, driven from **66 call sites**,
    each one a place where context supplies an expected type (a declared var
    type, a parameter, a return position, a struct-field initialiser, both arms
    of a comparison, an `Option`/`Result` payload through `?`, …).
  - `settleInt(e, hn)` / `settleFloat(e, hf)` — recurse through `Unary` and
    `Binary` so `-(1 << 40)` settles as a whole, and stamp `Width` /
    `IsUnsigned` on any literal still at `Width == 0`.
  - `checkLiteralFits(lit, t)` — diagnoses `E047 literal %d does not fit in %s`
    once a width is known.

So "settling" is not one function. It is a hint-propagation discipline threaded
through the checker's whole expression walk, plus a diagnostic.

## Size and shape of the port

The work is **not** "add a magnitude test to `check_expr`'s `ExprNumber` arm."
That would type `7000000000` as `i64` and fix the observed case, but it gets
the general problem backwards: it makes the literal's type depend on its own
text rather than on its use, so `var x: u64 = 3;` and `var y: f32 = 3;` stay
wrong, and `var z: i32 = 3000000000;` silently becomes an i64 assigned to an
i32 instead of the E047 diagnostic native gives.

A faithful port needs, in order:

1. **A polymorphic representation.** `parser.ExprNumber` needs the equivalent of
   `Width == 0` — an "unsettled" state distinct from i32. Today the self-host
   parser records `text` / `suffix` / `is_float`, so the suffix is already
   there; what is missing is a settled-width field and the convention that
   absent means polymorphic.
2. **A hint parameter through the checker's expression walk.** This is the bulk
   of it: `check_expr(e, s)` becomes hint-aware at the ~66 analogous contexts.
   The self-host checker's walk is structured differently from native's, so the
   count is indicative, not a target.
3. **Recursive settling through `Unary` / `Binary`**, matching `settleInt`.
4. **The fits diagnostic** (`E047`), so a literal that cannot fit its settled
   width is an error rather than a silent wrap.

## Why this is high-risk, and what would gate it

It moves the inferred type of **every unsuffixed integer literal in every
program**. That is the widest possible blast radius in the front end, against a
compiler whose primary correctness gate is a byte-identical self-compile.

Gates any attempt must clear, from `docs/TEST-GATES.md`:

- `internal/e2eselfhost` is **primary** — a literal-typing change is exactly the
  kind of thing the fixpoint is structurally blind to (a stable miscompile
  reproduces itself).
- Both 335-fixture self-host legs (`FERN_SELFHOST_FIXTURES=1`, wasm + x86-64),
  since the fixtures are where an accidental width change surfaces as a wrong
  exit code.
- The per-module fixpoint, because settling changes the compiler's own literal
  types and therefore its own emitted bytes.
- New checker tests for the E047 boundary in both directions.

## Recommended sequencing

Do it **after** the remaining carriers, not before. The carriers are
independently valuable, individually revertible, and each one narrows the set of
places the checker's answer is consulted — which makes a settling change easier
to evaluate, not harder. Settling is also the natural point at which
`irlower.literal_is_i64` (and its `parser.fern` twin) should be re-examined:
those exist precisely because lowering could not trust the checker, and a real
settling pass is what would let them be deleted.

That deletion is the honest success criterion. If a settling port lands and
`literal_is_i64` survives in both files, the port did not actually replace the
workaround — it added a third opinion about literal width.
