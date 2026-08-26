# Aliasing a param cost every CALLER its release — killer-drops slice 17

A callee that does nothing but ALIAS its parameter marked that parameter
non-borrowable, and the callers paid. Three callees against one caller, 20
rounds, the callee releasing nothing in any of them:

| callee body | caller |
|---|---|
| `return p.len() + o.len();` | 80/80 — flat |
| `var q: string = p; … q.len() …` | **80/40** |
| `var q: string = p; q = o; …` | **80/0** |

The whole loss is caller-side. It is the expensive half `2026-08-25-enum-value-block-borrow.md`
found for the match-expression read, one statement kind over.

## Two oracles, and the one I edited first is not the one that runs

`borrowable_params_of` and `borrowable_params_interproc` carry the same per-param
gate. The backends call the **interproc** one — `asm_ir.fern` computes
`bparams` from it at four sites and hands it to `fn_sigs_for_borrow`. The first
attempt here changed `borrowable_params_of`, built clean, and moved nothing;
what settled it was a diagnostic build that printed **no output at all**, because
the function is never reached on the emit path.

Worth the note because this file has the same split elsewhere: the move analysis
has `move_sites_toplevel_of` and `move_sites_of`, and mixing them has cost time
before. Check which one the backend calls before editing either.

## The gate

    if (!param_consumed_in_body(fn.body, pname, ip_reasgn)
        && !body_unsafe_for_match_borrow(fn.body, pname, reg, [])
        && !param_match_binding_escapes(fn.body, pname)) { … }

The `[]` is an EMPTY alias_ok, so `var q = p` reads as a bare-ident escape. The
forgiveness mechanism already exists — `stmt_unsafe_for_alias_vb` consults
alias_ok in its StmtVar arm, and gained a StmtAssign arm in
`2026-08-25-str-alias-reassign-counted.md` — and was simply never wired into this
verdict.

Two readings are now admitted as a UNION of independent proofs, not a relaxation
of either: the first forgives a bare-ident match scrutinee and stays strict on
aliases, the second forgives non-escaping aliases and stays strict on scrutinees.
A body with BOTH satisfies neither and stays refused. Both run under the current
registry, so the from-above fixpoint still shrinks monotonically.

## Two things the param verdict needs that a local's credit does not

**The alias sites drop `alias_bind_sites_of`'s reassigned-target exclusion.** That
exclusion protects a LOCAL's reclaim credit, where a slot reassigned before its
sweep would release the wrong box. The question here is only whether the PARAM
escapes, and `var q = p; q = o;` answers it plainly: q briefly aliases p, then
stops naming it.

**The REASSIGN sites are collected too.** That shape escapes `p` through the bind
and `o` through the assignment. Forgiving only the bind left the other half
refused — measured at 80/40, half the leak still there.

## Results

| shape | before | after |
|---|---|---|
| params read directly (control) | 80/80 | 80/80 |
| one param aliased to a local | 80/40 | **80/80** |
| both aliased, bind + reassign | 80/0 | **80/80** |
| caller reads its params back after churn | 200/120 | **200/200**, value 7 |
| escaping alias (negative control) | 80/40 | 80/40 |
| string accumulator (control) | 160/160 | 160/160 |

Native const-folds these literal concats and allocates nothing, so its counts are
not a comparison — its ANSWERS are, and they match on every row.

The churn row is the one that carries the soundness, and it has to live in the
CALLER. Every other failure in this wave was observable from inside the program
under test: a leak in the counts, an over-release in `__rc_underflow_count()`, a
use-after-read in the same function. Freeing a borrowed param's box corrupts
memory the caller owns and the callee exits clean. So that row reads the caller's
own strings back after the call with three fresh allocations in between, and
returns native's answer.

All 97 rc probes were run through the before and after compilers: three rows
moved, all above, every exit code unchanged. Sanitizer clean on all six shapes.
The self-host still compiles itself under `FERN_STRICT_IR=1`.

## The refusal that holds

An alias that ESCAPES by return (`var q = p; return q;`) keeps its param refused,
80/40 before and after. That is what makes this a carve-out rather than a blanket
accept: if that row ever reaches 80/80, the union has admitted a body whose alias
leaves the function and a caller is releasing a string its callee returned.

Pinned across x86 / arm64 / wasm by
`internal/e2eselfhost/self_host_aliased_param_borrow_test.go`.
