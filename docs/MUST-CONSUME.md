# `@must_consume` marker types — design

Status: [reference] shipped as E067 in the checker.

E067 checks explicit consumption of bindings whose types forbid implicit drop.
The check requires at least one consuming use on each checked path.
It does not establish exactly-once resource use.
Reference counting still manages memory, and the marker adds no runtime cost.

An `own` parameter declares a sink and is exempt from E067.
E050 rejects use of that parameter after consumption, but the sink can leave it unconsumed.
A resource API must implement the operation that fulfills its obligation.
The attribute alone does not prove that the implementation performs that operation.

## Surface

```fern
@must_consume
struct PendingResponse { status: i32, body: string }
```

`@must_consume` applies to struct and enum declarations (the
attribute slots into the existing `@derive`/`@import`/`@export`
allow-list in `parseAttribute`). A checked binding of a marked type requires
**at least one consuming use on every checked control-flow path** before scope exit.

**Consuming uses** (the value's obligation transfers or resolves):
- passing it as a call argument. A non-`own` marked parameter transfers
  the obligation to the callee. An `own` parameter declares an exempt sink.
  E051 still controls admission: a fresh construction, another `own`
  parameter, or an eligible local at its last use can pass.
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
- repeated consumption is outside the E067 guarantee.
  E050 separately checks uses after an `own` parameter moves.
  Neither check proves exactly-once behavior for an external resource.
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
   walk as `checkSliceEscapes` / `blockDiverges` — if/match arms
   each checked, loops treated conservatively (a consuming use
   inside a loop body counts; the zero-iteration path is a
   violation unless consumed after the loop too).
2. Field reads on the value are neutral (neither consume nor
   violate). A method call passes its receiver as the first argument,
   so it is a consuming use: the obligation moves to the method, which
   is tracked like any callee (a consuming receiver is the declared
   sink).
3. Storing into an UNMARKED container is reported as a violation
   at the store site ("obligation laundered into unmarked type") —
   this keeps slice 1 sound without inter-type obligation flow.
4. Closures capturing a marked value: violation in slice 1
   (capture semantics for obligations need design; forbid first).
5. New error code **E067**, `fern -explain E067` entry, message
   naming the type, the binding, and the unconsumed path's exit
   point.

## Self-host parity

Both checkers implement E067.
The Go oracle uses `internal/check/checker/mustconsume.go`.
The self-hosted compiler uses the `mc_*` functions in `compiler/checker.fern`.
The checker differential compares unfiltered diagnostic codes.
The `MC-*` entries in `spec/semantics.md` identify the portable conformance cases.

## First real users

`std/worker` uses the marker for `Worker` handles. See [WORKERS.md](WORKERS.md).

The original adoption plan listed these candidates:
1. `tcp_serve_supervised`'s worker lifecycle handle
   (`CRASH-ONLY-SERVE.md` D2') — "the child result must be waited";
2. a future `HttpResponseWriter` ("respond exactly once") if the
   serve-side API grows one;
3. `std/io` Writers ("close or flush before drop") — opt-in
   migration once the shape has soaked.

### Not `std/test`'s `TestRunner` — the shape slice 1 cannot see

`std/test` was the obvious fourth candidate: #8466 lost a recorded
test outcome to `r.it("case", body);` with the result dropped.
Slice 1 cannot express that rule, for two independent reasons.

- **The attribute is declaration-level.** `@must_consume` marks a
  `struct` / `enum`; putting it on a value-returning function is
  rejected outright ("@must_consume only applies to a `struct` or
  `enum` declaration"). "Mark the method, not the type" is not a
  surface that exists.
- **Marking the type does not catch the bug and breaks everything
  else.** The violation in the repro is a DISCARDED CALL RESULT,
  which the Rules section above explicitly leaves untracked —
  only bindings are walked. Meanwhile every receiver method
  `(r: TestRunner) it(...)` would take a marked parameter that it
  reads but never consumes, so the annotation reports E067 on the
  runner's own definitions before any caller is checked.

Adopting it here therefore needs the follow-up the Rules section
already names — E055-style checking of a discarded result whose
type is marked — plus a way for a receiver to be a non-consuming
borrow. Until both exist, the runner's guarantee comes from
`finish()` counting the TAP lines that reached the stream, not
only the results threaded back through the value
(`docs/TEST-RUNNER-MIGRATION.md`).

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
  in the E067 obligation walk — requires modelling defer's exit-edge
  semantics there. The `own`-parameter rule (E050/E051) already
  treats a deferred consuming call as the last use
  (`docs/security/HANDLE-AUDIT.md`, RH-06).
