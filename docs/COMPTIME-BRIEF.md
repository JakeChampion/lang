# Comptime design brief

Plan item **B2** of `docs/NICHE-BORROWS-PLAN.md`. Fern's standing
posture (`LANGUAGE-DIRECTION.md`) is that comptime is **deferred**
"until we feel the pain of separate generic + const systems." This
brief does not change that — it records, while the research is
fresh, the rules any future Fern comptime must obey and the
architecture it should copy, so the eventual design starts from
these constraints instead of rediscovering them. Sources: the
niche-language research (`NICHE-LANGUAGE-RESEARCH.md`) — Zig's
comptime restrictions (sourced from matklad's analysis of what
comptime deliberately won't do) and Lean 4's staged
metaprogramming pipeline (verified against the CADE-28 paper and
the metaprogramming book).

## The Zig rules (what comptime must NOT do)

Zig's comptime earns its keep through restrictions, not powers.
Four rules, each with a Fern-specific reason:

1. **Target-faithful.** Comptime code observes the TARGET's
   layout (usize width, endianness), never the host's. For Fern
   this rule is existential, not stylistic: every Fern compile is
   a cross-compile (wasm32 `WidthPtr`=4 vs arm64/x86-64
   `WidthPtr`=8), so a comptime that byte-inspected a pointer or
   folded `sizeof`-shaped arithmetic against the HOST would
   miscompile every multi-target program. Concretely: comptime
   evaluation must resolve `WidthPtr`-dependent facts through the
   same per-target resolution the IR uses (`Op.Width` sentinel →
   backend), or refuse to evaluate them.
2. **Hermetic — no I/O whatsoever.** Not even sandboxed reads.
   Comptime evaluation must be reproducible and cacheable; this
   is also what keeps the self-host fixpoint gates meaningful
   (a comptime that read a file could make gen1 ≠ gen2 byte
   output legitimate, destroying the bootstrap oracle).
   Build-time codegen that needs external data belongs in
   tooling (a generator writing `.fern` sources), not comptime.
3. **One mechanism: partial evaluation of ordinary code.** No
   token-tree macros, no string mixins, no separate template
   language. Zig covers reflective printing, generic containers,
   and compile-checked format strings with `comptime` parameters
   + `inline for` + type reflection. Fern already has
   monomorphised generics and const-folding (`internal/constfold`)
   — a comptime should GROW from unifying those two, not arrive
   as a third system beside them (that unification pain is
   exactly the trigger condition LANGUAGE-DIRECTION.md names).
4. **Accepted costs, named up front.** Zig's trade-offs apply to
   any partial-evaluation design: no declaration-site type
   checking of comptime code (errors surface per-instantiation),
   and comptime-computed types can't grow methods. If Fern wants
   deriving-style API synthesis, that stays with `@derive` (the
   attribute path), which already exists and composes with
   traits.

## The Lean 4 pipeline (the architecture to copy)

Lean's metaprogramming architecture — verified: user-defined
syntax rules and elaborators in ordinary user code, the meta
layer itself ~90% Lean — is far more machinery than Fern needs.
The **copyable core** is the staged two-tree shape:

    parse   →  Syntax   (concrete syntax tree)
    macros  →  Syntax → Syntax   (rewrites, before/during elab)
    elab    →  Expr     (typed core)

Fern's equivalent today: parse → AST, desugars → AST → AST,
check → typed AST → IR. The gap is that Fern's desugars
(f-strings, `use`, `for-in`, `assert`, `todo`, the pipe
placeholder, tuple-match in the self-host parser) are **inlined
inside `parseX` functions** rather than structured as passes.

**The one actionable rule this brief sets (adopted now):** new
parse-time desugars of non-trivial size should be written as
explicit AST→AST rewrite functions with a single call site,
rather than woven through recursive-descent code. This costs
nothing today, is 80% of a macro system's internal architecture,
and keeps the eventual comptime/macro door open. (Existing
desugars migrate opportunistically — when one is next touched,
extract it; no big-bang refactor.)

## Trigger conditions (when to revisit)

Design a real comptime only when at least one of:
- generics + constfold demonstrably fight (a feature needs both
  systems extended in incompatible ways);
- a stdlib surface needs compile-checked value-level input
  (format strings, route patterns) badly enough that runtime
  checking is a measured cost;
- the self-host compiler itself would delete significant code by
  computing tables at compile time.

Until then: `@derive` for API synthesis, monomorphisation for
type-level genericity, `internal/constfold` for value folding,
tooling for codegen.
