# Array.build / Map.build — the scoped linear builder

Date: 2026-06-08.
Status: design. Picks the implementation approach for the builder that
`docs/PURE-COLLECTION-API-PLAN.md` §4 specifies. No compiler code changed
by this doc.

## Why

The pure-collection API (E055 + the value-returning `insert`/`append`/
`with` rename) removed the silent mutate-and-discard footgun, but it makes
a naive append-in-loop **O(n²)** on aliased data and allocation-heavy even
when unique:

```fern
var out: i32[] = [];
for x in xs { out = out.append(x.abs()); }   // each append may copy `out`
```

The standard fix (Clojure transients, Roc opportunistic reuse) is a
**scoped, linear builder** whose mutability provably can't be observed or
aliased, so it does not reintroduce the cycle vector or the lost-write
footgun. It is also the prerequisite for **E056** (rejecting `arr[i] = v`):
the genuine in-place-mutation sites that remain (machine-code encoders'
`buf[at] = byte`, in-place sorts) need a performant value-returning
replacement before subscript assignment can become a compile error.

## Surface

```fern
var out: i32[] = Array.build(function(b: ArrayBuilder[i32]): void {
    for x in xs {
        b.append(x.abs());     // statement — in-place append to b
    }
});                            // freezes into an immutable i32[]
```

- `Array.build(fn: (ArrayBuilder[T]) -> void): T[]`
- Builder methods: `append(v: T)`, `with(i: i32, v: T)` (both **statements**,
  in-place), `len(): i32`.
- `Map.build` / `MapBuilder[K, V]` (`insert`, `without`, `len`) is the
  symmetric follow-up; this note focuses on `Array.build` first.

Inside the builder closure, `b.append(x);` / `b.with(i, x);` are bare
statements (their whole job is the side effect), so **E055 does not apply**
to them — this is the one place imperative accumulation stays legal, and it
is provably local.

## Approach — parse-time desugar to a unique-local IIFE

Two architectures were considered:

1. **A first-class `ArrayBuilder[T]` runtime type** — a mutable 1-slot box
   holding the array pointer (like `Cell`), with `append`/`with`/`len`
   lowered as in-place ops, `build` allocating the box + calling the
   closure + freezing. Clean model, but requires new IR on every backend
   (box alloc, the closure call inside `build`, empty-slot init) and
   self-host parity work — a large multi-backend surface.

2. **A parse-time desugar** (CHOSEN) — `Array.build` is pure surface sugar.
   `ArrayBuilder[T]` never reaches the checker/IR; it is consumed by the
   desugar, which lowers to existing constructs:

   ```fern
   Array.build(function(b: ArrayBuilder[T]): void { BODY })
   ```
   →
   ```fern
   (function(): T[] {
       var b: T[] = [];
       BODY'              // each `b.append(x);` → `b = b.append(x);`
                          // each `b.with(i,x);` → `b = b.with(i, x);`
       return b;
   })()
   ```

   The rewrite `BODY → BODY'` retargets only **statement-position** calls
   `b.append(...)` / `b.with(...)` (where `b` is the closure's parameter)
   into reassignments of the local `b`; `b.len()` and every other read stay
   verbatim. The rewrite recurses into nested `for`/`while`/`if`/block
   bodies (the append is usually inside a loop).

### Why the desugar is sound and fast

- **Correctness.** Even if `b` were somehow aliased, `b = b.append(x)` is
  the ordinary value-returning append — CoW copies on a shared buffer, so
  no write is ever lost. The desugar reuses the already-tested
  array-literal / append / move-on-reassign machinery; nothing new in the
  checker, IR, interp, or any backend.
- **Performance.** `b` is a fresh local that escapes nowhere (the rewrite
  only ever reassigns it; `return b` hands out the single reference on the
  way out), so its buffer is rc = 1 throughout and every `b.append` /
  `b.with` takes the existing in-place fast path — O(1) amortised append,
  no copy. This is the unique-path Fern already emits, with the alias case
  made unreachable by construction instead of checked.
- **E055 exemption falls out for free.** After the rewrite there are no
  discarded value-returning results — `b = b.append(x)` is an assignment.
  No special-casing of E055 inside `build` is needed.
- **Captures work.** Outer names the body references (`xs` above) become
  ordinary captures of the IIFE closure, which closures already support.
  The direct-IIFE call shape is already exercised (`direct-iife` e2e).

### Trade-off vs. the first-class type

The desugar gives up a *compile-time* linearity guarantee: a pathological
body (`var alias = b; ... ` then appending to both) would silently CoW
rather than be rejected. That is a performance cliff, not a correctness
bug, and it is exactly what `fip` (E053) is for (§5 of the collection
plan). If a hard linearity guarantee is later wanted, the first-class type
(approach 1) can replace the desugar without changing the surface. v1
takes the pragmatic, low-risk path.

## Trigger + element type

- The parser recognises a call whose callee is `Array.build` (an
  `Array`-qualified `build`) with a single lambda argument, and performs
  the desugar there (sibling of `parseMapLit` / `parseForEach`).
- `T` comes from the lambda parameter's declared type `ArrayBuilder[T]`
  (its single type argument), which also types the `var b: T[] = []`.
- A non-lambda argument, a missing/!= 1 parameter, or a parameter whose
  type is not `ArrayBuilder[_]` is a parse error with a clear message.

## Phasing

1. **`Array.build` desugar + `append` + `with` + `len`** — ✅ **done.**
   Go parser desugar (`maybeDesugarArrayBuild`) + parser tests + e2e
   (append-while / append-for-in / with-elem-set / len-read / churn on
   x86-64 / wasm / interp). Because it desugars to existing constructs,
   no checker/IR/backend changes.
2. **Self-host `parser.fern` parity** — ✅ **done.** The same desugar in
   `parser.fern` (`maybe_desugar_array_build`, byte-level prefix match —
   no `std/string` import), so the self-host compiler handles `Array.build`
   on every backend. Verified: `array-build` in the self-host prog
   (x86/arm64) + wasm-run suites, and an `array_build` differential case
   (Go vs self-host, x86-64 / arm64 / wasm — identical output).
3. **`Map.build` / `MapBuilder`** — ✅ **done (Go).** Same desugar
   (`maybeDesugarMapBuild`): `b.insert(k, v);` → `b = b.insert(k, v)` on a
   fresh `map_new(8)`-backed local. `insert` + `len` (the value-returning
   `without` returns a tuple, so it's deferred). Parser test + e2e
   (insert-while / insert-for-in / len-read / string-keys-overwrite /
   churn on x86-64 / wasm / interp). *Self-host `parser.fern` parity:
   follow-up* (mirrors the Array.build self-host port).
4. **Migrate every `arr[i] = v` site + land E056** — ✅ **done.** All ~83
   indexed-assignment statements (stdlib base64 / hex / sort / string / i32
   / core/int / task / test; self-host asm encoders / ssa / interp / vm /
   watbin; examples word_freq) rewritten to `arr = arr.with(i, v)` — the
   CoW unique-in-place fast path on an rc=1 local, same perf as the old
   write. **E056** (`*ast.Assign` with an `*ast.Index` target rejected;
   compound `arr[i] += v` too) makes subscripts read-only, the counterpart
   of E048. The genuine in-place mutators (`fip` insertion sorts) stay
   allocation-free: **E053 accepts `.with` on an `own` receiver** as the
   method-call form of the in-place write. Tests: `TestArrayElement-
   ImmutabilityE056`, fip `.with`-on-`own`/non-`own`, e2e. (Go reference
   compiler; self-host E056 enforcement is deferred with E048/E049 —
   `IMMUTABILITY-MIGRATION-PLAN.md` §4 "Remaining" — but the self-host
   *source* is fully migrated and `.with` works on every backend.)
   Finishes `docs/PURE-COLLECTION-API-PLAN.md` §3a.

## What this doc IS / IS NOT

- IS: the chosen implementation strategy (desugar) + surface + soundness +
  phasing for the builder.
- IS NOT: the `Map.build` details (sketched only) or the `fip`/E053
  performance-pinning policy (owned by the collection plan §5).

https://claude.ai/code/session_01PH4qvcoqNB5HrdAVFX6BAZ
