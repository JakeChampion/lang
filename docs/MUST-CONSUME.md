# `@must_consume` marker types — design

Plan item **E1** of `docs/NICHE-BORROWS-PLAN.md`, from the
niche-language research: Vale's "Higher RAII" and Austral's linear
types show that a type which **forbids implicit drop** can carry
compile-time obligations — respond to this request exactly once,
close this socket, commit or roll back this transaction. Rust
cannot express this (its types are mandatorily affine — everything
is auto-droppable); it needs only a per-type undroppable marker,
not a borrow checker. Fern is unusually well placed: the checker
already runs an intra-procedural escape family (E063 slice-escape,
E065 str-escape), and RC keeps doing the actual freeing — the
marker is purely a checker-side obligation, zero runtime cost.

## Surface

```fern
@must_consume
struct PendingResponse { status: i32, body: string }
```

`@must_consume` applies to struct and enum declarations (the
attribute slots into the existing `@derive`/`@import`/`@export`
allow-list in `parseAttribute`). A VALUE of a marked type must be
**consumed exactly once on every control-flow path** before its
binding goes out of scope.

**Consuming uses** (the value's obligation transfers or resolves):
- passing it as a call argument (the obligation moves to the
  callee: a non-`own` marked param is itself tracked in the
  callee, while an **`own` param is the declared SINK** — exempt
  from E067, with `checkOwnedParams`' affine rule providing the
  at-most-once half, so own-boundaries get exactly-once. Note the
  owned-argument rule means `own` params accept only fresh
  constructions or other own params — local-held values discharge
  by return / match / marked-container store / passing to non-own
  params);
- returning it (the caller inherits the obligation);
- constructing another value with it (struct literal field, enum
  payload, array element, tuple element — the obligation rides
  along inside the container, which should itself be marked for
  the chain to stay checked; unmarked containers launder the
  obligation and are reported, see Rules);
- destructuring it (`match` on a marked enum consumes the
  scrutinee; the payloads become ordinary values unless
  themselves marked).

**Violations**:
- a marked local (or parameter) reaching scope exit / function
  return without a consuming use on some path;
- overwriting a binding that still holds an unconsumed marked
  value (`x = fresh();` while old `x` unconsumed — the old value
  is implicitly dropped);
- consuming the same binding twice on one path is NOT an error
  Fern can fully check without move tracking — slice 1 checks
  at-least-once, not exactly-once; the RC runtime keeps
  double-use memory-safe regardless (it's an obligation checker,
  not a memory-safety checker).
- a DISCARDED call result of a marked type (`sink(t);` where sink
  returns the marked value and the result is dropped) is not
  tracked in slice 1 — only bindings are. E055-style
  discarded-result checking is the natural follow-up.

## Rules (slice 1 — deliberately E063-shaped)

Intra-procedural, conservative, no false negatives on the shapes
it claims:

1. Walk each function. For each local binding / parameter whose
   type is marked: require every path from the binding site to
   function exit to contain ≥1 consuming use of the binding.
   Implemented as the same style of conservative divergence-aware
   walk as `checkSliceEscape` / `blockDiverges` — if/match arms
   each checked, loops treated conservatively (a consuming use
   inside a loop body counts; the zero-iteration path is a
   violation unless consumed after the loop too).
2. Field reads / method calls on the value are neutral (neither
   consume nor violate).
3. Storing into an UNMARKED container is reported as a violation
   at the store site ("obligation laundered into unmarked type") —
   this keeps slice 1 sound without inter-type obligation flow.
4. Closures capturing a marked value: violation in slice 1
   (capture semantics for obligations need design; forbid first).
5. New error code **E067**, `fern explain E067` entry, message
   naming the type, the binding, and the unconsumed path's exit
   point.

## Self-host parity

**Ported (both checkers).** The native walk landed first (#4933,
with self-host parse-tolerance so marked programs compiled), and
the `checker.fern` port followed as its own slice (the `mc_*`
family, mirroring `internal/checker/mustconsume.go`; deviations in
`SELFHOST-CHECKER-PORT.md`). Historical note: the port originally
targeted the gate's per-code opt-in set
(`selfHostImplementedCodes`), but that filter was DELETED the same
day (freeze precondition 3, #4451) when the checker port reached
full coverage — the differential gate now compares the raw,
unfiltered code sets, with 21 `mc-*` fixtures covering E067.

**Status: the self-host port has landed.** The parser stamps the
attribute onto `StructDecl.must_consume` / `EnumDecl.must_consume`,
`checker.fern`'s `mc_*` family mirrors the native walk, and `"E067"`
is in `selfHostImplementedCodes` with an `mc-*` fixture corpus in the
differential gate. See docs/SELFHOST-CHECKER-PORT.md (E067 entry) for
the port's shape and its two self-host-specific mappings (variant
constructor calls as EnumLit stores; the value-position match/if IIFE
desugar treated as an inline block rather than a capture).

## First real users

Ships with test types only, until a real API adopts it. Queued
adopters, in order:
1. `tcp_serve_supervised`'s worker lifecycle handle
   (`CRASH-ONLY-SERVE.md` D2') — "the child result must be waited";
2. a future `HttpResponseWriter` ("respond exactly once") if the
   serve-side API grows one;
3. `std/io` Writers ("close or flush before drop") — opt-in
   migration once the shape has soaked.

## Deliberately deferred

- Exactly-once (move) checking — needs use-after-move tracking;
  at-least-once catches the forgotten-obligation bug class, which
  is the valuable half.
- Inter-procedural obligation summaries ("this callee consumes
  arg 2") — every call transfers in slice 1, which matches RC
  ownership-transfer semantics anyway.
- Obligation-carrying containers (marked-inside-unmarked) —
  rule 3 rejects instead of tracking.
- `defer`-based discharge (`defer x.close();` as a consuming use)
  — natural follow-up; requires modelling defer's exit-edge
  semantics in the walk.
