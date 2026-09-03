# 2026-09-03 — `own` struct params: released at exit, donated with string / enum fields (#5342)

The issue asked for own-param reuse donors with enum / string fields, which
§7 of `../SELFHOST-PERCEUS-REUSE.md` recorded as blocked on a freshness proof a
parameter cannot give. Measuring the shapes first showed the reuse gap was the
smaller problem: the self-host LEAKED every row where native is clean, and two
of the leaks had an over-release behind them.

One widening in this area is **unsound and is not landed** — see "The argument
temp" below. It passed every small-program gate and segfaulted gen1 of the
per-module emit-all fixpoint.

## Measured

x86-64, `FERN_LEAKCHECK=1`, 100 rounds, allocs/frees/live_bytes. `w(i)` is a
`@noinline` producer of a non-SSO string; every callee is `@noinline`. "before"
is this commit's parent, rebuilt on the same tree (a leak comparison across two
toolchains measures the toolchain).

| shape | native | before | after |
|---|---|---|---|
| `bump(p: P): P { return P { ...p, n: p.n + 1 }; }`, `P { s: string, n: i32 }`, `bump(P { s: w(i), n: i })` | 400/400/0 | 400/100 live 16800 | 400/200 live 12000 |
| the same with `own p` | 400/400/0 | 300/100 live 12000 | **300/300/0** |
| `bumpq(own p: Q): Q`, `Q { e: E, n: i32 }`, `enum E { A(i32), B(i32) }` | 300/300/0 | 300/0 live 13600 | **200/200/0** |
| control `N { m: i32, n: i32 }`, `own p` | 200/200/0 | 100/100/0 | 100/100/0 |
| the control BORROWED | 200/200/0 | 200/100 live 4800 | **200/200/0** |
| `sink(own p: P): i32 { return p.n + p.s.len(); }` | 300/300/0 | 300/100 live 12000 | **300/300/0** |
| `sink_void(own p: P): void { … falls off the end }` | 300/300/0 | 300/100 live 12000 | **300/300/0** |
| `id(own p: P): P { …; return p; }` | 300/300/0 | 300/100 live 12000 | **300/300/0** |
| `f(i): void { var xs: i32[] = [i, i + 1]; … falls off }` | 100/100/0 | 100/0 live 4000 | **100/100/0** |
| `relabel(own p: A, t)` over `A { xs: i32[], s: string, n: i32 }` (reuse refused) | 700/700/0 | 700/200 live 29600 | 700/300 live 24000 |

The self-host pairs the `own` string and enum rows into the param's box, so its
alloc counts sit one below native's; native allocates the result fresh there.

The first row is the one that stays open: its call RESULT is released now, its
argument TEMP is not.

## Four leaks, two of them over-releases in disguise

**The call result.** Every row shares one cause: `var q = bump(…)` earned no
strict-fresh credit, because `return_value_is_strictfresh_struct` refused any
literal with a base. Yet every field a `T { ...base, … }` CARRIES reaches the
new box counted — the base-copy path retains a nested-struct or enum field
unconditionally and a string field under the routing verdict, and a scalar
carries no reference. `spread_carried_fields_counted` admits exactly that;
arrays, tuples, maps, options and closures copy uncounted and still refuse. A
bare `return p` of an `own` param joins on the frame-fresh terms
(`own_param_ret_is_frame_fresh`: never reassigned, aliased, stored, passed on,
no field copied out uncounted).

The routing half of that predicate is load-bearing and was wrong in the first
draft: `spread_copy_field_counted` claimed a string field always copies counted,
where the base copy retains one only under `slit_reclaim`. An unrouted
string-fielded type spread as a base mints an uncounted co-owner, and calling it
counted hands two owners one buffer. (Corrected before landing; it was not what
the fixpoint caught — that was the argument temp below, which the bisect
separates.)

**The callee's own param.** A parameter was never exit-swept, so an `own` struct
param whose reuse was refused (the enum row) was never released at all.
`own_struct_param_release_rows_of` credits an `own` struct param the frame still
holds at every exit — `"OWNREL:"` deep, `"OWNRELB:"` box-only where a field may
have been copied out uncounted — and `emit_dec_sweep_except_list` releases it. A
donating reuse site zeroes the slot, so the release finds null there. The
`moves_fields_*` walker gained `spread_counted`, the caller's word that a
`T { ...p }` over this type copies every field counted; without it a string-only
param spread as a base would have been demoted to box-only and its string
stranded.

**The void fallthrough.** A void body that falls off its end left through the
backend's default epilogue with no sweep — every owned local of every such
function leaked, own params included, while the same body ending in `return;`
was clean. `lower_func` now emits the sweep before that exit. This is why
`void_local` (no `own` param at all) moves.

Then the two that were not leaks:

- **An `own` position read as borrowable.** `borrowable_params_of` flagged a
  param borrowable whenever the body did not "consume" it, `own` or not. So the
  caller stashed and FREED a fresh argument the callee had taken — the scalar
  control's 100/100 was that free cancelling the never-released `q`, with `q`
  read after the free. Widening the temp gate alone turned it into exit 99.
  An `own` position is a move by declaration; both flag sites now say '0'.
- **The own-update string admission on an unrouted type.** `own_update_params_of`
  admitted a string-fielded param regardless of routing, and the reuse arm
  `__fern_str_free`s the overridden field's old value. The caller's construction
  retains a string share only for a type that ROUTES field reclaim, so on an
  unrouted type the callee freed the caller's string under a live holder. The
  witness needs a field NAME the routing scan refuses program-wide
  (`get(x: P): string { return x.s; }` poisons every `s`), a share
  (`bump(P { s: h.s, n: i })`) and allocation churn before the read:

  ```
  parent, leakcheck : exit 77   (h.s.len() changed after the free)
  parent, sanitize  : exit 12   (the quarantine stops the recycling that exposes it)
  fixed             : exit 12, 1800/1600
  ```

  A green sanitize leg is not evidence against a use-after-free whose victim is
  read but never written; it is 2026-09-02's lesson read the other way round.

## The argument temp: the widening that segfaults gen1, and is NOT landed

`stash_fresh_struct_arg` admits scalar-only ∪ `struct_fields_reusable`, so a
string-fielded literal handed to a borrowing callee leaks its box and its
string. Widening it to every type that routes field reclaim — and releasing
through the exit sweep's own rc-gated field drop — closes the first row's
remaining 200 blocks and is what the borrowed row would need.

It is unsound. `call_arg_borrowable` proves the callee keeps no reference to the
BOX; it says nothing about a FIELD the callee hands out. `get(x: P): string
{ return x.s; }` escapes only because it is not borrowable at all, and a callee
that returns a field while otherwise qualifying has its string freed under the
value it handed back.

Nothing local sees it. The measured result:

| gate | verdict with the widening |
|---|---|
| the 17 rows of this change, both legs, `FERN_SANITIZE` | green |
| reuse differential + exclusions + fip census (137 subtests) | green |
| the rule-13 string/enum shape sweep (105 tests, both leak matrices) | green |
| `TestSelfHostPerModuleEmitAllFixpointX86_64` gen1 | **SIGSEGV** |

Bisected against the fixpoint, one part at a time (A = the strict-fresh
widening, B = this temp widening, C = the OWNREL exit release, D = the void
sweep):

| config | fixpoint |
|---|---|
| A B C D | FAIL |
| B C (A, D off) | FAIL |
| B (A, C, D off) | FAIL |
| none | PASS |
| A C D (B off) | **PASS** |

So B is necessary and sufficient, and A / C / D are green in every combination.
The toggle leaves the new rc-gated release in place on both sides, so the
release shape is not the fault — admitting the wider TYPE set is. Closing it
needs a callee-side "hands out no field" proof the self-host does not compute
today; until then the shape keeps its leak rather than trading it for a
use-after-free. `borrowed_spread_string` pins the residue at frees=200, so a
retry of the widening fails there rather than in a fixpoint an hour later.

## The reuse ask, and why no bind literal is needed

`donor_enum_fields_fresh` exists so the LOCAL families can free an overridden
enum / string field's old value without freeing under an uncounted alias, and it
proves that by reading the donor's literal. For an `own` parameter the question
has a different answer: the box is sole-owned by the contract (the runtime
`__fern_rc_is_unique` guard backstops), and its field values carry whatever
count the caller's construction gave them. Every non-fresh enum field store
retains (the ExprStructLit enum arm has no routing gate; a struct with a direct
enum field always routes), every base copy retains, and a bind out of the field
is counted — so an old enum box's rc counts every live holder and the flat
rc-gated dec never frees under one. A string field is retained on the same terms
only for a routed type, which is the one condition
`struct_fields_reusable_ownparam` adds. The override / recipient values take
`cross_recipient_fields_fresh`, the gate every cross family shares.

Admitted: `own_param_reuse_sites` (cross), `own_param_self_overwrite_sites`
(`var c = T { ...own_d, f }`) and the return-position / self-assign update
(`own_update_params_of`) for enum fields and routed string fields. Pinned by the
eight `own-param-*-{string,enum}-field*` differential cases, the
`aliased-string-own-override` / `unrouted-string-own-donor` exclusions, and
three census rows (each `fresh=0 paired=1`).

## Witnessed versus contract

- The routing condition on the own-update admission is WITNESSED:
  `unrouted_string_own_update_refused` fails on the parent with exit 77 and
  passes fixed.
- The own-position move is witnessed on the widened temp gate (exit 99 with the
  gate alone) and on `own_scalar_control`, which the parent balanced by accident.
- The counted-share argument for enum fields is witnessed once on the local
  family (`enum_field_bind_then_local_override`, bind out then override:
  balanced, underflow 0) and on the own rows; it is contract-only for a share
  taken through a path this log did not enumerate.
- `own_array_spread_refused_reuse` pins the box-only demotion: 700/300, no
  underflow, where the deep release would free the array the result carries.
- The routing clause inside `spread_copy_field_counted` is CONTRACT-only: no row
  here distinguishes it, and it was corrected by reading the retain it pairs
  with, not by a measurement.

## Traps this one set

- **A green sanitize leg is not evidence against a use-after-free.** The
  unrouted-string bug reads the freed box and never writes it; the quarantine
  suppresses the recycling that exposes it, so `FERN_SANITIZE` reported a clean
  leak line while the census leg returned 77.
- **The fixpoint is the only gate that saw the temp widening.** 137 targeted
  subtests, 105 shape-swept tests and both leak matrices were green with it in.
  Run the per-module fixpoint before believing any change that adds a release —
  the same escalation #7548 and the 2026-09-02 entry record.

## Side finding, not fixed

The self-host checker accepts
`var p: P = P { s: w(i), n: i }; var q: P = bump(p);` where native rejects it
with E051 (`ow_is_owned_expr` admits a local bound to a fresh construction;
native admits only the construction itself or another `own` param). On the
parent that program ran 300/300 by the same accident as the scalar control; now
that the callee honours `own`, the caller's sweep and the callee's release meet
on one box: exit 99, and `use-after-free` under FERN_SANITIZE. The program is
ill-typed by the language's rule, so this is the checker's gap, tracked
separately.
