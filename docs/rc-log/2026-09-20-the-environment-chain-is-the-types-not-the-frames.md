# 2026-09-20 — the environment chain is the type's, not the frame's

#9804, filed off a review of #9800 as "costs a refusal, closing it is small".
Half right: closing it is small. It did not cost a refusal.

## What it cost

```fern
struct Holder { f: (i32) => i32 }
function make(n: i32): Holder {
    var xs: i32[] = [n, n + 1, n + 2];
    return Holder { f: (x: i32): i32 => { return x + xs[0] + xs[2]; } };
}
function apply(h: Holder): i32 { return h.f(1); }
function main(): i32 { … var h: Holder = make(i); t = t + apply(h) % 3; … }
```

```
FERN_SEM_IR: module: produced 4 of 4 declarations and 0 of 0 instances
error: __sem_drop_Holder did not lower: conflicting drop helper for
__sem_drop_Holder — the IR path bailed in this function and there is no AST
emitter to fall back to (#8590)
```

The module produced WHOLE. The disagreement is between two copies of the
record's drop helper — `make` names the function type and emits the full
chain, `main` reaches it only as a field and emits an empty one — and
`merge_helpers` refuses the pair at emit time, after the substitution into
the cache has already happened. There is no AST body to stand in for a
helper, so the compile fails. A struct with a closure field, built in one
function and dropped in another, did not compile on the default path.

## The fix

`semsource.env_rows_closed` closes the rows over the schema table: build the
table, collect the function types every record and enum field carries —
through arrays, tuples, Maps and Cells, since a release walks into those —
take their rows, append the rows' environment types to the named set, and
repeat until a round adds nothing. An environment is a record whose captures
may themselves hold function values, which is why it is a fixpoint and not a
second pass.

`ssarc.env_records` keeps its contract unchanged and drops the paragraph
saying the stronger claim was not yet in force.

## Measured

Both legs, `FERN_SANITIZE=1`, 200 rounds:

| leg | produced | answer | leakcheck |
|---|---|---|---|
| semantic | 4 of 4 | 4 | allocs=600 frees=600 live_bytes=0 |
| AST (`FERN_SEM_IR=`) | — | 4 | allocs=600 frees=600 live_bytes=0 |

The case joins `semProductionPrograms` as
`closure-field-built-and-dropped-apart` with `noLeak: true`, which is the
absolute pin: the produced bodies hold nothing at exit on the sanitize
target.

## Trap

**"Produced N of N" is not "compiled".** The tally is printed before the
helpers merge, and a helper conflict is the one refusal that arrives after
it. Reading the tally as the module's fate is how a compile failure read as
a false refusal in the issue that named it.
