# A consumed parameter is counted, a read one borrows

2026-09-21 — `ownership.fern`, `semsource.with_inferred_modes`,
`semlower.inferred_pass`. Closes #9891. Refs #4451.

## What it does

`semsource.mode` made a reference parameter counted only where it was declared
`own`. Everything else borrowed, so no frame could reclaim what it was handed.
`ownership.escaping` now answers which parameters a function CONSUMES, and the
contract carries that instead.

A value is carried out when it is returned, built into a container or box,
stored into a cell or a map, or passed to a slot its callee counts. A parameter
is consumed when it, or anything anchored to it, is carried out.

It is a GREATEST fixpoint: every reference parameter starts consumed and is
demoted once a round finds no carrying use. A least one answers "borrowed" for
every recursive traversal — the only use that carries the parameter out is the
recursive call, whose own slot is counted only if the parameter is, so the
cycle never starts from below.

It reads the PRODUCED graphs, not the declarations: `semsource` appends a
parameter's mode and hands it back untouched while it builds a body, never
branching on it, so the graph is the same whichever answer this reaches.

## The corpus

x86-64, callgrind, retired instructions, against the same tree without it.

| | before | after | |
| --- | ---: | ---: | ---: |
| `pvec_with` | 681,006,132 | 219,077,188 | **0.322x** |
| `record_update` | 332,888,005 | 255,883,196 | **0.769x** |
| `map_probe_chain` | 231,536,735,687 | 204,918,388,047 | **0.885x** |
| 26 other rows | — | — | 1.000x |

No row is slower. All 29 exit codes match. `pvec_with` was 3.69x native and is
now **1.187x** (native SSA: 184,540,118), with its 476,249 array clones gone
and no stdlib annotation — the analysis finds both `__pv_with_in`'s node and
`PVec.with`'s receiver on its own.

Compile time is at parity: 155.8s against 160.4s for the whole compiler. A
first cut cost +23% by remaking `ssasem.analyze` per function per round; the
analysis does not depend on the rows, so the caller holds it.

## The four rungs, and what each cost to learn

Every one of these was found by `internal/e2eselfhost` and by nothing else —
not the bench sweep, not the targeted gates, not the compiler bootstrapping
itself. TEST-GATES says that suite is PRIMARY for self-host lowering changes;
this is what that means in practice.

**1. Only an enum or a record.** A bare string or array parameter costs and
wins nothing: 1.049x on `ascii_scan`, 1.025x on `utf8_ingest_validated`. Native
has the same rung first (`ownedByDefaultShapeIn`). Arrays and strings are still
reclaimed as the CHILDREN of an admitted box.

**2. A body any function VALUE names keeps its declared modes.** A counted
parameter is spelled in the type `ssasem.closure_type` builds, and that type is
what every call through the box is checked against, so inferring one rewrites
the type out from under the call sites: 57 refusals reading "closure
signature", and because a refusal spreads to everyone who calls the refused
body, the compiler's own module went to **0 of 8772** produced. Native has the
rung for the mirror-image reason — `OpCallIndirect` has no callee name, so no
retain is emitted there at all. I argued the self-host did not need it because
the mode rides the type. The type riding the mode is exactly why it does.

**3. The module must lower whole.** A mode inferred here is invisible to the
AST lowering, which reads `own` off the declaration and nothing else. In a
mixed module an AST-lowered caller hands a borrowed argument to a callee that
now releases one: `field-append` answered 3 where it answers 199. A declared
`own` is safe there precisely because it is a SOURCE fact both lowerings read.

`semsource` cannot see this. The refusal that makes a module mixed — "calls X,
which the AST lowering defines" — is emitted by `semlower`, after production,
as is `FERN_SEM_IR_SKIP`. So the inference moved to `semlower`, which asks for
it once `prune` has decided what it will emit and keeps it only if the module
still lowers whole under the new modes. The second lowering is what that costs,
and it is never paid where it would hurt: a module with anything left to the
AST lowering fails the test on the FIRST pass.

Widening `irlower.own_consumed_positions` — the channel that already tells an
AST caller which ARRAY positions a produced body consumes — was the
alternative, and it is the wrong direction: it invests in the AST lowering that
goal 2 is retiring.

**4. A parameter whose value reaches a LENT slot is not counted.** This is the
one that cost a day, and the bisect is the finding.

The compiler built with the inference segfaulted on the smallest input, at
`semsource.ret` -> `semtypes.equal` -> `semtypes.numeric_equal`, reading a null
type. The pre-inference sources pass the same test, so it was this change's.
Restricting the inference to structs alone still crashed, so it was not the
enum payload take.

Bisecting by row (a temporary `FERN_OWN_LIMIT`, in the shape `FERN_SEM_IR_SKIP`
already establishes — 8505 rows, fourteen builds) put the boundary at
`semsource.stmt`, and the emitted diff at that boundary is eleven added
`__sem_drop_ast__Stmt` calls, one per match arm:

```fern
ast.StmtVar(v)  => { return declare(st, v, s); },
ast.StmtExpr(e) => { return discard(e.value, s); },
```

Every arm destructures `st` and hands a payload onward before that drop runs. A
slot the callee does not count is LENT, and nothing in this pass can see what
the callee does with what it was lent: `hands_out` models a call result as a
FRESH unit, which severs the result's dependency on the argument. A callee that
puts a lent value in what it returns is therefore invisible here.

That is harmless while the caller borrows the container too, because then
nobody drops it. Counting the container is what makes it fatal — the frame's
drop frees a box the result still points at.

The rule removed the last three regressions as well as the crash:
`ordmap_insert` 1.020x -> 1.000x, `pmap_insert` 1.017x -> 1.000x,
`map_probe_chain` 1.019x -> 0.885x. Those were exactly the shapes paying an
inc/dec for a container whose payload goes to a lent slot.

## Traps, for the next person

- **Owned-by-default without the inference is a REGRESSION**, not a staging of
  it: slower on nine of twenty-nine rows and faster on none, 1.901x on
  `ordmap_insert`. Native could stage 2 before 2d because its 2a was narrow;
  the blanket form is the widened end state without its optimisation. Measured
  in the sibling entry.
- **A syntactic substitute does not work.** "Counted when the body matches the
  parameter and binds a reference payload" is local and needs no call graph,
  which is tempting because contracts are built per declaration. But
  `enum_match`'s `weigh` binds `Label(s)` where `s` is a string, so the rule
  catches the pure reader and keeps its 7.9%. The distinction that pays is
  whether a payload is CONSUMED or only READ, and that is an escape property.
- **Stdlib import cycles are allowed** (`std/i32` <-> `std/string`), so
  contracts cannot be ordered bottom-up. `semsource.contracts` is already a
  fixpoint over every declaration in the flattened module, which is why the
  analysis fits there and why both sides read one table by construction —
  native maintains that agreement by convention across `paramOwnedByDefault`
  and `calleeParamOwnedByDefault`.
- **A test that passes either way is not a gate.** The receiver case took three
  attempts. `Holder { tag: h.tag, items: h.items.with(at, v) }` allocates 32
  either way, because `projection_root` already admits a borrowed parameter as
  the root of an in-place field update. A callee that destructures and rebuilds
  does not move either, since that parameter does not escape. Only a callee
  that hands its parameter BACK reads the receiver's mode — 123 to 93.

## What it does not reach

The inference is an ESCAPE analysis, which is the right criterion for whether
the caller's retain and the callee's release cancel, but it is narrower than
"every FBIP rebuild". A destructure-and-rebuild that never lets the parameter
out stays borrowed and gets no in-place update.

`decl_param_mode`'s receiver rung is gone, so a receiver is now inferred like
any other parameter. The unconditional borrow was also what kept a `dyn` vtable
method's receiver borrowed; `ssasem` and `semsource` carry no `dyn` or trait
handling at all today, so nothing reaches that path through this pipeline, and
the rung native needs (#6465) is owed the moment one does.
