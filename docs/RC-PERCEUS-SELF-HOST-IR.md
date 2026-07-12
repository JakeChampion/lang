# Would a native-shaped IR help the self-host Perceus port?

Status: **superseded** — this early feasibility note answered "yes"; the
actual rebuild + port are tracked in `RC-PERCEUS-SELF-HOST-IR-REBUILD.md`
(design + rollout) and `RC-PERCEUS-SELF-HOST-PORT.md` (the active goal-2
tracker). Kept as the original assessment (2026-06-09).
Question raised: the native (Go) compiler lowers AST → IR (`[]Op`) and
the self-host compiler goes AST → asm directly with no IR. Since the
remaining Perceus work (precise drops, move cancellation, reuse tokens,
borrow inference) is unported on the asm backends, *would rebuilding the
self-host around native's IR shape make that port easier — or possible?*

Short answer: **it would not meaningfully ease the analysis port — the
native Perceus analyses are already AST-level — but it would attack a
different, real cost: the triplicated emission layer.** Decide it on
that basis, not on "the analyses need an IR." They don't.

---

## 1. The premise to correct

It is tempting (this note's author included) to assume native's
Perceus passes operate on the `[]Op` IR, keyed on a linearized op
stream, value numbering, and dominance — and that the self-host can't
reproduce them without the same structure.

**That is not what the native code does.** Every Perceus *decision*
analysis reads `b.fn.Body` — the **AST** — and emits side-tables keyed
by local name / `*ast.Ident` / `*ast.Expr`. The `[]Op` IR is the
*output* of lowering; these analyses run before/during lowering and
*guide* it. Concretely:

| Native analysis | Reads | Produces |
|---|---|---|
| `computePreciseDrops` (`ir.go:4076`) | `b.fn.Body.Stmts`, `ast.Walk` | `map[stmtIdx][]name` |
| `computeMovedLocals` (`ir.go:4541`) | `b.fn.Body`, `ast.Walk` | `map[name]bool` |
| `markConstructionMoves` (`ir.go:4685`) | `ast.Expr` + pre-order `*ast.Ident` index | mutates `moved` |
| `computeReuseSources` (`ir.go:12945`) | `b.fn.Body` | `map[ast.Expr]name`, `set` |
| `computeFreeEligible` (`ir.go:3327`) | AST + escape table | `set` |
| `inferParamEscapes` (`ir.go:2089`) | call graph (AST) | `map[fn][i]bool` |

The "pre-order index" these passes use is the order `ast.Walk` visits
`*ast.Ident` nodes; the "dominance" is *top-level statement* position
in `fn.Body.Stmts`. Both are AST facts. None of it is `[]Op`-keyed.

This is exactly why `RC-PERCEUS-SELF-HOST-PORT.md` §5 already specifies
each analysis as "a pure function over the (monomorphised) AST + the
type oracle" — that is a faithful mirror of native, not a workaround
for the self-host lacking an IR.

### Proof by transliteration

`computePreciseDrops` ports onto the self-host AST with no IR, because
the self-host `FuncDecl.body: Stmt[]` and statement/expression structs
(`StmtVar`, `StmtReturn`, `StmtAssign`, `ExprIdent`, …) are the same
shape native walks. Sketch (shared, `asmcore.fern`):

```fern
// compute_precise_drops(fn, s) -> map stmtIdx -> names to drop after that
// top-level statement. Mirror of native computePreciseDrops (ir.go:4076).
pub function compute_precise_drops(fn: parser.FuncDecl, s: EmitState): DropTable {
    var stmts: parser.Stmt[] = fn.body;
    var decl_idx: StrIntMap = strintmap_new();
    var reassigned: StrSet = strset_new();

    // 1. record first declaration index; flag any redeclaration/assignment.
    var i: i32 = 0;
    while (i < stmts.length) {
        match (stmts[i]) {
            parser.StmtVar(v) => {
                if (decl_idx.has(v.name)) { reassigned.add(v.name); }
                else { decl_idx.set(v.name, i); }
            },
            parser.StmtAssign(a) => { reassigned.add(a.target); },
            _ => {}
        }
        i = i + 1;
    }
    // assignments at ANY depth rebind the slot -> conservative bail (native
    // does the same via ast.Walk over the whole body).
    walk_assign_targets(fn.body, reassigned);

    // 2. for each owned, free-eligible, non-moved, uniquely-named local whose
    //    init is a fresh owned value (not a counted alias), find its last
    //    top-level use and schedule a drop right after it.
    var out: DropTable = droptable_new();
    for (name in decl_idx.keys()) {
        var di: i32 = decl_idx.get_or(name, 0 - 1);
        if (reassigned.has(name)) { continue; }
        if (s.moved_locals.has(name)) { continue; }
        if (!s.free_eligible.has(name)) { continue; }
        if (s.reuse_consumed.has(name)) { continue; }
        if (!precise_droppable_type(name, s)) { continue; }
        // init must be a fresh owned value, not an uncounted/counted alias.
        match (stmts[di]) {
            parser.StmtVar(v) => {
                if (init_may_alias_live(v.init, s)) { continue; }
                if (needs_rc_inc_on_alias(v.init, s)) { continue; }
            },
            _ => {}
        }
        var last: i32 = 0 - 1;
        var unsafe: boolean = false;
        var j: i32 = di + 1;
        while (j < stmts.length) {
            if (stmt_references(stmts[j], name)) {
                if (flows_into_uncounted_alias(stmts[j], name, s)) { unsafe = true; break; }
                last = j;
            }
            j = j + 1;
        }
        if (unsafe) { continue; }
        if (last < 0) { last = di; }                       // declared, never used
        if (is_control_flow_stmt(stmts[last]) && !safe_for_control_flow_drop(name, s)) { continue; }
        if (is_return_stmt(stmts[last])) { continue; }       // handled by return lowering
        out.add(last, name);
    }
    return out;
}
```

This is line-for-line the native function with `*ast.Var`→`StmtVar`,
`ast.Walk`→the explicit `match`/`walk_*` helpers the self-host already
uses. **No IR appears anywhere.** The same is true of `compute_moved_locals`,
`compute_reuse_sources`, and the escape/free-eligible analyses. The
self-host already has the side-table-on-`EmitState` machinery
(`local_names`, `local_types`, the ported `move_on_return_idx`) these
plug into.

**Conclusion for the analyses: an IR buys nothing.** The port is the
same amount of work with or without one, because native itself doesn't
use an IR for these decisions.

---

## 2. What an IR *would* actually buy: emission deduplication

The self-host's real structural cost is downstream of the analyses.
Native emits RC operations **once**, during AST→IR lowering, as
`OpCallDirect` to named runtime helpers (`__fern_rc_inc`,
`__fern_rc_dec`, `__fern_alloc_reuse`, generated `__drop_*`). All three
backends then instruction-select a single opcode form. One emission
site, three trivial selectors.

The self-host has **no shared lowering target**, so every RC emission
is hand-written three times — in `asm.fern`, `asm_arm64.fern`, and
`wasm.fern`:

- alias-inc at reference-creation sites,
- the function-exit dec sweep (with move-on-return exclusion),
- per-type drop handlers,
- (eventually) reuse-token emission, CoW gates, consuming-match.

Today only `move_on_return_idx` + array alias-inc are wired, and they
already live in two hand-maintained `emit_*` mirrors. Every *future*
Perceus emission slice (precise drops, reuse tokens, drop
specialization for struct/enum/tuple, borrow-aware CoW) multiplies by
the backend count. That is the parity-drift surface CLAUDE.md warns
about, and it is the one thing an IR removes: lower RC to an opcode
once, select it per backend.

So the honest framing of the trade is:

> An IR does **not** make the Perceus *analyses* portable (they already
> are). It makes the Perceus *emission* write-once instead of
> write-three-times.

---

## 3. Three options, ranked

### Option A — Full native-shaped IR rebuild
Give the self-host native's `[]Op` IR: AST→IR lowering, then rewrite all
three `emit_module`s to consume IR. RC becomes `OpCallDirect`, emitted
once.

- **Pro:** strongest long-term convergence with native; *all* future
  codegen (not just RC) shares one lowering and diffs against native;
  emission de-triplicated everywhere.
- **Con:** large, partly-throwaway rewrite. The backends currently
  consume AST directly — this rewrites their entire emit layer, not
  just RC. Forces a decision on native's **mutable builder** vs the
  self-host's **immutable threaded `EmitState`** (adopt mutability =
  determinism hazard for the byte-identical bootstrap; build an
  immutable IR = diverge from native, losing the mechanical-port
  benefit). Stalls the green-slice Phase 0–6 plan already shipping.
- **Verdict:** right call *only if* the goal is "self-host becomes a
  faithful twin of native across the whole backend," pursued as its own
  project with its own doc — not justified by Perceus alone.

### Option B — Continue the current AST-pass plan (status quo)
Port each analysis as an AST function (§1) and hand-mirror the emission
across the two/three backends, as `move_on_return` already is.

- **Pro:** lowest risk, already underway and green. The safe-leak
  invariant + runtime `__fern_rc_is_unique` net mean a mis-port leaks,
  never UAFs.
- **Con:** every emission slice is written N times; ongoing parity-drift
  vigilance in the `emit_*` mirrors.
- **Verdict:** correct if the priority is *finishing RC* with continuous
  green and minimal architectural churn.

### Option C — Narrow shared RC-emission layer (recommended)
Keep AST→asm. Don't build a general IR. Instead introduce a **single
shared list of RC emission directives** the analyses produce and a thin
per-backend interpreter emits — i.e. an IR *only for the RC ops*, not
for the whole program.

Shape: the analyses already produce per-function side-tables. Add one
more output — an ordered `RcOp[]` (`RcInc(slot)`, `RcDec(slot)`,
`DropStruct(ty, slot)`, `ReuseToken(d, c)`, …) attached to statement
indices — defined once in `asmcore.fern`. Each backend implements one
`emit_rc_op(op)` selector (the `__fern_rc_*` call it already hand-writes
today), and the shared driver walks the directive list at the right
points. This is exactly the `OpCallDirect`-for-RC slice of native's IR,
without lowering the entire program.

- **Pro:** captures ~80% of Option A's emission-dedup benefit (the RC
  surface, which is the part that multiplies) for a fraction of the
  cost; no whole-backend rewrite; no mutable/immutable global decision;
  stays inside the existing Phase 0–6 plan. De-triplicates the part that
  actually grows slice over slice.
- **Con:** still AST→asm for everything non-RC; doesn't give the broader
  "diff whole codegen against native" benefit of Option A.
- **Verdict:** best balance. Recommend adopting this as the emission
  model for Phase 4–5 (precise drops, reuse, drop specialization),
  where the triplication cost first bites hard.

---

## 4. Recommendation

> **Decision (2026-06-09): Option A was chosen** — the full
> native-shaped IR rebuild, for whole-backend convergence with native
> (not just RC). The rollout lives in
> `docs/RC-PERCEUS-SELF-HOST-IR-REBUILD.md`; slice 0 (the `Op` data
> types, `examples/self_host/ir.fern`) has landed. The analysis below
> still stands as the feasibility record — note its §1 finding (the
> Perceus *analyses* are AST-level and need no IR) carries into the
> rebuild: the IR's payoff there is **emission de-triplication**, not
> analysis porting.


1. **Don't rebuild the self-host around native's IR to make the
   analyses portable** — they're AST-level in native, so the IR adds no
   leverage there. The §1 transliteration is the actual port shape and
   it needs no IR.
2. **Do** treat emission triplication as the real cost, and adopt
   **Option C** (a shared `RcOp[]` directive layer in `asmcore.fern` +
   one `emit_rc_op` selector per backend) before Phase 4, so precise
   drops / reuse / per-type drops are written once.
3. **Revisit Option A only** if a separate goal emerges — making the
   self-host a whole-backend twin of native for diffing and shared
   optimization — in which case it deserves its own design doc and is
   not gated on Perceus.

Net: the IR question and the Perceus-port question are *less coupled*
than they first appear. The port proceeds on the AST either way; the
only thing genuinely on the table is *how RC is emitted*, and Option C
answers that without a rebuild.
