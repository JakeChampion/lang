# 2026-09-28 — an enum self-rebind through a counted handback keeps its release (#10447, part 1)

AST lowering (`FERN_SEM_IR=`) only. The typed lowering already balanced every
row here.

#10447 lists seven positions where an enum's count is stranded on the AST
lowering. This entry closes the two self-rebinds:

```fern
function hb_rc_param(e: Rc): Rc { return e; }   // an "ENUM:" member: the return retains
cur = hb_rc_param(cur);
cs = hb_sc_param(cs);                           // the scalar-payload twin
```

## Cause

Each rebound local lost its credit altogether.

- **An rc-payload local bound from an `RCE:` call** (`let cur: Rc = mk_rc(r)`)
  is the fresh family, `collect_fresh_rcenum_names`. That family admits a
  reassigned local only when `all_assigns_fresh_rcenum` finds every rebind is
  a fresh chain. `hb_rc_param(cur)` is not one.
- **A local bound from an `ENUM:` call** (`let cs: Sc = mk_sc(r)`) is
  `ECALL:`, and `collect_counted_enum_local_sites` refused any reassigned
  local, because the assign path had no counted-enum release.

## Change

All in `examples/self_host/irlower.fern`.

- `enum_handback_rows` adds `ENUMHB:<fn>|<positions>` to `opt_fresh_ret_fns`
  for each free `ENUM:` member. The positions are the parameters it returns
  bare (`bare_handback_rows`).
- `counted_self_handback` admits `name = f(..)` into the fresh family when
  every one of `f`'s handback positions receives `name` itself. The result is
  then `name`'s own chain with one more count. The rebind's
  `emit_enum_deep_reinit_store` is `__fern_rc_is_unique`-gated, so it drops
  that count back to 1 and leaves the payload alone.
- `all_assigns_counted_enum_call` admits a reassigned `ECALL:` local when every
  reassignment is an `ENUM:` member call. Each such result carries its own
  count.
- The scalar-enum rebind store now passes `rebind_call_same_dec` to
  `emit_arr_store_from`. A counted handback returns the same box, and the
  old reference must still be released rather than skipped by the
  same-pointer guard.

## Measured (`FERN_LEAKCHECK=1`, AST lowering, x86-64, allocs / frees)

| row | before | after |
|---|---|---|
| `rc_self` (the issue's `cur = hb_rc_param(cur)`) | 200 / 0 | 200 / 200 |
| `sc_self` (the issue's `cs = hb_sc_param(cs)`) | 100 / 0 | 100 / 100 |
| `rc_self_twice` | 200 / 0 | 200 / 200 |
| `rc_self_then_fresh` (a handback rebind, then a fresh one) | 400 / 0 | 400 / 400 |
| `sc_other` (`cs = hb_sc_param(o)`) | 200 / 0 | 200 / 200 |
| `rc_other` (guard: hands back another local's chain) | 400 / 0 | 400 / 0, pinned |
| `rc_pick_mixed` (guard: two handback positions, one the local) | 400 / 100 | 400 / 100, pinned |
| `sc_cond` (a handback or fresh call in each branch of an `if`) | 250 / 50 | 250 / 250 |
| `sc_loop_call` (a counted call rebind inside a nested `while`) | 300 / 0 | 300 / 300 |
| `sc_cond_alias` (guard: `cs = o` inside a branch) | 200 / 100 | 200 / 100, pinned |

`sc_cond_alias` is what the branch arms of `stmt_assigns_counted_enum_call`
exist for: without them the alias is admitted, and the sanitizer reports a
use-after-free.

`TestSelfHostEnumSelfRebind{X86_64,Arm64,Wasm}` holds every row to the
interpreter's answer under both lowerings. The x86-64 leg also runs
`FERN_SANITIZE=1`.

## Not covered

Seven shapes remain on the AST lowering, each with its own cause. The typed
lowering balances all of them.

- a cross-local handback between two rc-payload locals
  (`cur = hb_rc_param(o)`, the `rc_other` row);
- a two-position handback where only one position is the local
  (`cur = hb_rc_pick(cur, o, ..)`, the `rc_pick_mixed` row);

- a method receiver (`mk_sc(r).val()`): there is no borrow proof for an enum
  receiver;
- an array-literal element (`[hb_rc_param(y)]`);
- a handback from a match arm (`hbm_rc`);
- a struct-literal temporary with an enum field, passed at a borrowable
  position;
- a lender consumed by a match while the handback result is still live.
