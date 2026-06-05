# Plan: copy-on-write Maps in the interpreter (M1)

Scoping document for the deferred **M1** finding from
`docs/ADVERSARIAL-REVIEW-2026-06.md`. Status: **planned, not yet
implemented.**

## Problem

The interpreter is the differential **reference oracle**: 16 e2e files
run a program through `interp` *and* every backend and assert they agree
(`feature_differential_test.go`, `numeric_property_test.go`, the
`fuzz-diff` jobs, …). That only works if the interp is a faithful
independent implementation.

For `Map[K, V]` it is not. The interp's `Map` is a `*Map` shared on
assignment and mutated in place (`internal/interp/interp.go`:
`builtinMapSet` / `builtinMapDelete` / `builtinMapClear`), while every
compiled backend does **copy-on-write** (`core/map.fern`
`__map_cow_inplace`). So:

```fern
var m = map_new(8); m = m.set("a", 1);
var n = m;                 // alias
n = n.set("a", 999);       // mutate the alias
m.get_or("a", -1)          // interp: 999   backend: 1
```

`fern -interp` and the compiled backends return **different results** for
the same program — the oracle disagrees with what it is supposed to
certify.

## The observable contract (what "match COW" means)

The backends use **reference-counted COW** (Perceus). The observable rule
is *not* "maps are value types" and *not* "maps are functional"; it is:

> A mutating method (`set` / `delete` / `clear`) mutates the receiver
> **in place** when the map has a single live reference (rc == 1), and
> **copies** when it is shared (rc > 1).

Two consequences that any faithful interp must reproduce, both pinned by
existing passing tests:

1. **Bare mutation works when unshared.** `m.set(1,10); m.len()` returns
   1 — `TestInterpMapBasic` "len after set" and the `map_ops` case in
   `TestFeatureDifferential` rely on it, and the differential test
   passing proves the *backends* mutate in place here too (rc == 1).
2. **Aliases are isolated.** After `var n = m`, a mutation through `n`
   (or `m`) does not leak to the other (rc == 2 → copy). This is the case
   the interp currently gets wrong.

## Why the cheap shortcuts don't work

Each was checked against the contract and **introduces a new divergence**
(worse than the documented status quo, which is a single known one):

| Shortcut | Breaks |
| --- | --- |
| **Always-copy** in set/delete/clear (functional) | Loses bare-statement mutation: `m.set(1,10); m.len()` → 0 in interp, 1 on backend. Breaks `TestInterpMapBasic`. |
| **Copy-on-assignment** (`var n = m` deep-copies) | Diverges on bare-set-after-alias: `var n=m; m.set("b",2); m.get_or("b",-1)` → interp 2 (m unshared copy, in-place), backend -1 (n still refs it, rc==2 → copy). |
| **Sticky "shared" flag** (once aliased, always copy) | False copies after the alias dies: `{ var n=m; } m.set(1,10); m.len()` → interp 0 (still flagged shared), backend 1 (rc back to 1, in-place). |

The common thread: the backend's behavior depends on the **exact rc at
the moment of the mutating call**, so the interp needs real reference
counting, not an approximation.

## Required mechanism: rc on the interp Map

Add `rc int` to `Map` and make the mutators COW on it:

```go
// set/delete/clear:
if m.rc <= 1 { /* mutate in place, return m */ }
else        { nm := m.clone(); /* mutate nm */; return nm }
```

`rc` must count **live references** to the `*Map`: every binding (env
var, struct field, array/tuple element, closure capture) plus the
in-flight value. It is incremented when a reference is created and
decremented when one dies.

### The hard part: move vs. alias

`rc++` must fire on an **alias** (`var n = m;` while `m` stays live) but
**not** on a **move** (`var m = map_new();` / `var m = m.set(...)` — the
producer's reference transfers to the new binding). The interpreter does
not currently know which a binding is — that is exactly the affine
information the checker computes for `own` params (E050 use-after-move)
and the backend's Perceus pass turns into dup/drop. Getting move-vs-alias
wrong in either direction is observable:

- Over-count (treat a move as an alias) → inflated rc → false copy →
  breaks bare-set (contract point 1).
- Under-count (treat an alias as a move) → premature in-place → alias
  leak (contract point 2, the original bug).

## Value-flow hook points

Every place a `*Map` reference is created or destroyed (file refs in
`internal/interp/interp.go`):

| Event | Location | Action |
| --- | --- | --- |
| `var x = <init>` | `*ast.Var` case → `env.declare` (1863) | inc **iff** `init` is an alias (Ident / field-read of a live map), not a move (call/literal). Also `*ast.Destructure`, match-arm bindings, `for` loop var, `let`/`if let`. |
| `x = <v>` | `evalAssign` Ident/Index/FieldAccess (`env.set` 1852) | release old slot value (dec, **skip if old == new** — self-assign from in-place set), acquire new (inc iff alias). |
| call `f(m)` | `callFunc` param bind (1894-1897) | inc each map arg (param is a new live ref); dec on function-scope exit. |
| `return m` | `callFunc` (1916) | **escape**: the returned value transfers to the caller — must *not* be dec'd by the callee's scope-exit. |
| `Foo{f: m}` / `[m, …]` / `(m, …)` | struct/array/tuple literal eval | inc for the slot; dec the contained maps when the container is dropped (**recursive drop** — the interp relies on Go GC and does no recursive teardown today). |
| closure capture of `m` | lambda/closure env build | inc; dec when the closure is dropped. |
| block end | `execBlock` (after the stmt loop) | dec every local in `e.vars` that holds a map (respecting the return-escape above, and early `flowReturn`/`flowBreak`/`flowContinue` exits). |

The **recursive container drop** and **escape on every early exit**
(`flowReturn` / `flowBreak` / `flowContinue`, `?`-operator early return,
`defer`) are where this becomes a faithful Perceus port rather than a
couple of hooks.

## Two implementation options

**Option A — interp-side rc with an init-expression move/alias
heuristic.** Inc only when the binding's source expression is a pure
alias (`*ast.Ident` / field read), treat call/literal results as moves,
plus the self-assign and return-escape special cases. Bounded, but the
container-drop and early-exit cases must each be handled and are
divergence-prone.

**Option B — consume the checker/backend move analysis.** Annotate the
AST (or a side table) with the dup/drop points the backend's Perceus pass
already computes, and have the interp follow them exactly. Cleaner
semantics (no heuristic), but more plumbing to surface that information to
the interp. Likely the more robust long-term shape, and it keeps the
oracle definitionally in lockstep with the backend's own RC decisions.

Recommendation: prototype Option A behind the validation gate below; if
the container/escape cases prove too fragile, switch the rc source to
Option B.

## Validation strategy

`wasmtime` is now installable locally (v34.0.1 + `wasm-tools` 1.225.0 +
the WASI command adapter — see the toolchain note in CLAUDE.md), so the
**full differential oracle, including the wasm backend, runs locally**.
That is the gate:

1. The entire differential + interp suites must stay green (no new
   divergence) — run with the wasm toolchain on `PATH` and
   `FERN_WASI_ADAPTER` set so nothing SKIPs.
2. Add targeted differential cases for each contract edge: bare-set when
   unshared, alias isolation (the M1 repro), bare-set-after-alias,
   alias-then-scope-exit-then-bare-set, map passed to a function and
   mutated, map returned from a function, map stored in a struct/array
   then mutated through an alias, map captured by a closure.
3. Only merge when 1 + 2 are green. A partial rc that breaks a
   currently-passing differential test is a regression and must not ship
   — the known single divergence (status quo) is strictly better than an
   unpredictable set of new ones.

## Scope estimate

Medium-to-large. The rc field + COW decision + clone is small; the
faithful inc/dec discipline across all flow points (esp. recursive
container drop and early-exit escape) is the bulk, and the validation
matrix above is what de-risks it. Sequence it as its own change with the
differential gate, not folded into unrelated work.
