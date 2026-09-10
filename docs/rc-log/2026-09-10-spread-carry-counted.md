# A spread copy owns the arrays it carries

Refs #8983. `main` went red at `Test e2e self-host / selfhost-fixpoints-x86_64`
after #8982: the self-built compiler (gen1 of the emit-all fixpoint) aborted
with exit 134 on `-per-module-func-counts` while the native-built gen0 ran the
same query fine. Silent, because `__fern_oob_abort` prints nothing.

## Where it trapped, and why that was not the bug

gdb on the aborting gen1, walking the `rbp` chain from the trap:
`checker.Scope.lookup` indexing `s.types[i]` with `i` valid for `s.names` —
one scope whose two parallel arrays had different lengths — under
`check_expr` ← `check_call_expr` ← `check_block` (a lambda body) ←
`annotate_expr` ← … ← `ensure_capture_types_parts`.

First-parent bisect over `f1b98ce..36ca992` on the fixpoint test: #8982 is
the first bad merge. But the compiler's own emit had not regressed: emitting
the same (HEAD) sources with the last-good self-host compiler and with HEAD's,
7 of 5375 functions differ, all in `irlower` (`lower_call_method`,
`lower_call_named`, `lower_stmt_inner`, `mcis_stmt`, `sa_escape_stmt`,
`walk_expr_escapes`) plus `run_per_module`, and splicing the last-good text of
all seven into HEAD's units still aborts at the same frame. So the miscompile
is in code both compilers emit identically, exposed by a shape #8982 added to
the checker: `annotate_lambda_expr` binds a lambda's parameters on a scope
built as `Scope { ...s, loop_depth: 0, ret_type: rt }` and then
`ls = ls.bind(name, ty)` per parameter.

## The shape

The base copy of a `T { ...base, … }` retained a nested-struct or enum field
and a routed string field, and carried every ARRAY field uncounted — a fresh
rc-1 box co-owning `base`'s buffers without a count. Two consumers take a
unique box to own its fields:

- the receiver-field append (`s.names.append(name)` in `Scope.bind`) tests
  `__fern_rc_is_unique` on the receiver box and, when unique, moves the buffer
  out of it and pushes in place — into `base`'s buffer;
- the consume-rebind reclaim (`ls = ls.bind(…)`) frees, through
  `__field_reclaim_Scope`, the fields the successor replaced — `base`'s
  `types` buffer, later reused under `base`.

Together: the caller's scope read a `names` that had grown and a `types` that
had been freed. Minimal program, self-host x86-64, before the fix:

```
struct Sc { names: string[], types: i32[], depth: i32 }
function (s: Sc) bind(n: string, t: i32): Sc { return Sc { names: s.names.append(n), types: s.types.append(t), depth: s.depth }; }
function lam(s: Sc, ps: string[]): i32 {
    var ls: Sc = Sc { ...s, depth: 0 };
    …  ls = ls.bind(ps[i], i + 10);  …
}
```

exits 91 (the caller's `s.names.len()` is no longer 3); native is right.

## The fix

The base copy retains every un-overridden array field it carries, on both the
per-field path and the `op_struct_copy` compact path, so an append through the
copy finds the buffer shared and un-shares it instead of growing the base's.
The one exception is the self-rebind spread `o = T { ...o, … }`: its successor
replaces the base in the same slot and the reclaim's cow-skip hands the base's
single count to the successor (#6653), so the carry stays uncounted there and
only there (`base_is_selfrebind`).

What the copy may then release DEEP is a second question, and the first
attempt got it wrong by answering "everything retained". The runtime's
un-share copy (`__fern_arr_push` on an rc ≥ 2 receiver) copies a pointer-element
buffer with its children unsecured — the open pointer-element contract of
#8874 — so after `ls.bind(…)` the copy's `types` shared its enum boxes with
the base's, and the copy's exit drop (`__fern_arrarr_free` at rc 1) freed them
under the base. Measured on the sanitizer build as a use-after-free in
`__fern_arrarr_free`'s element walk, reached from `annotate_lambda_expr`'s
exit sweep, and without it as a segfault in `decl_field_type` a pass later.
So `spread_copy_field_counted` counts a carried array only when it is a
scalar-element kind (`is_leaksafe_array_field`); a string[] / struct[] / enum[]
carry is retained but not counted, exactly the line
`return_value_is_strictfresh_struct` already draws for an override. The box
then earns no fresh credit, its rebinds do not reclaim, and the retained count
is released only with the base's: the same leak the pre-#8982 alias form
(`var ls = s; ls = ls.bind(…)`) had, and no free.

`is_fresh_struct_init` was the other gap: it called every struct literal a
sole-owner box, spread or not, so a spread carrying an uncounted kind was
credited fresh and reclaimed deep. It now asks `spread_carried_fields_counted`
like the return-side predicate already did; that needs the string-field routing
registry, so `sfok` is threaded through the 35 predicates between
`reclaimable_names_of` / `return_fresh_struct_ret_fns_of` and it.

The `spread_sites` refusals (`*_share_holder_respread`) and the derived-local
box-only demotion were written against the uncounted carry. They stay: the
self-rebind spread still carries uncounted, and a refusal is a leak, never a
free. Their comments now say which spread they are about.

`own_array_spread_refused_reuse` (own-param release) moves from an exact 300
frees to balanced 700/700 at live 0: the result now holds its own count of
`xs`, so the param's release stays deep and the result's drop pairs with it.

The counted carry also retires two demotions that were written against the
uncounted one, or the base's own count leaks instead. The fixture
`alloc_flat_struct_self_update` caught it (`fork_base`: `var b = Buf { ...a, … }`
printed `grows`, 400/300 per shape census): `derived_anywhere` marked `a`
NODEEP because a value derived from it was bound elsewhere, and
`moves_fields_stmts` was asked with `spread_counted = false` at the fresh-local,
frame-fresh, snapshot-local and receiver-borrow sites, so a spread over `a`
read as a field move. Both now ask `spread_copy_counted` /
`spread_carried_fields_counted` for the local's type (`spread_over_counted` for
the derivation, which also requires every override reading the base to be
scalar-typed); `recv_borrow_fns_of` takes `sfok` for that. All five shapes of
the fixture census at allocs == frees, live 0.

## Next lead

Securing element children on the un-share copy (and releasing them on the last
unique drop) is what lets a pointer-array carry count, and with it the
`Scope`-shaped locals reclaim instead of leak. That is #8874's remaining
contract, not this change.

## Witnessed

- `TestSelfHostAliasReassignReclaimIRX86_64/spread-carry-owned`: the program
  above, 91 → 0, with `__rc_underflow()` checked.
- `TestSelfHostPerModuleEmitAllFixpointX86_64`: the failing gate, green.
- The targeted reclaim / reuse / own-param / field-append suites, plus the
  four #8982 re-pins (`two_declarations`, `binder_shadows_array`,
  `self-assign-shadowed-by-var`, `local-root-shadowed-name-grows`).
