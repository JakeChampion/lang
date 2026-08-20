# A map local at a tuple element had no owner at all

`var t: (i32, Map[K, V]) = (i, m)` cost `m` its whole reclaim, and nothing on
the tuple side took over. Measured on the TODO list's worst remaining parity gap
(192 B/round self-host, 0 native).

## What was measured

`churn` at 200 rounds, x86-64, `FERN_LEAKCHECK=1`:

| shape | native | self-host before | self-host after |
|---|---|---|---|
| `var m = map_new(4); var t = (i, m); t.0 + t.1.get_or(..)` | 0 | 12 800 (64 B/round) | 64 (flat) |
| the same, plus `m = m.insert("k", i)` | 0 | 38 400 (192 B/round) | 12 928 (64 B/round) |
| `(Map, Map)` — two maps | 0 | 25 600 | 128 (flat) |
| `([i, i+1], Map)` — array element too | 0 | 12 880 | 144 (flat) |
| the same map with NO tuple (control) | 0 | 64 | 64 (unchanged) |

Every case's exit code was adjudicated against `bin/fern -interp` AND the native
x86-64 backend, and folds `__rc_underflow_count() * 100` in: on all 18 cases and
all three backends the self-host answer equals native's, so nothing here is an
over-release wearing a fixed leak's clothes.

## Mechanism

`m` at a tuple-element position is caught by BOTH positional gates on the "MAP:"
exit set — `alias_idents_in_value` credits the alias, `expr_unsafe_for` calls the
element an escape. Instrumented at the gate rather than reasoned about:

```
PROBE map=m nonself=0 aliased=1 unsafe=1 iter=0 ident=0     # (i, m)
PROBE map=m nonself=0 aliased=0 unsafe=0 iter=0 ident=0     # no tuple
```

Nothing on the tuple side compensates:

* tuple construction emits **no rc_inc** for a map slot (`slot_is_rc_container`
  does not cover one), so the tuple holds a BARE, uncounted pointer;
* `emit_tuple_child_drops` skips a bare-ident element by design;
* `emit_tuple_type_child_drops` can never see one — `tuple_arg_payload_fresh`
  refuses a Map-typed position outright, so "TUPRCS:" is unreachable for such a
  tuple.

So exactly **one** release is owed and the local is what owes it. This is NOT
#7184's string interlock, where construction retained to rc 2 and the credit AND
the element release were both required; doing both here double-frees.

## The fix

`map_tuple_elem_borrow_only` overrides the two positional gates — and only those
two — when every alias and every escape of the map is a bare-ident element of a
tuple that cannot outlive it. A host qualifies when it is a `var`-bound local
(never an assignment: its target may be declared in an enclosing scope), is not
reassigned, does not itself escape, carries a tuple ANNOTATION, and lets no
NON-SCALAR position back out (`tuple_pos_borrow_only`: every `host.<i>` read is
the receiver of a `map_recv_borrows` method). Non-scalar rather than
Map-typed, because a map reached through a type parameter or an alias is not
spelled `Map[` — and a scalar position carries no pointer to extract, which is
all the relaxation `t.0` needs.

It is asked only when the plain reading has already refused. It walks the body
again, and the maps it can rescue are the minority that get that far.

`used_only_as_tuple_elem` answers both gates with one walk, in the shape
`body_unsafe_for_clo` already established — skip the host's `var` statement,
recurse through if / while / for / match before skipping those.

The PRECISE drop keeps refusing, deliberately. Its drop point is the map's last
TOP-LEVEL use, which IS the tuple binding, so it would free the box before
`t.1` is read. Only scope exit is past every read of a local tuple.

## The trap this set

The first version overrode the alias/escape gates and nothing else. It made the
headline shape flat and **segfaulted** two others:

```
var t: (i32, Map[string, i32]) = (i, m);
return t.1;                       // exit 139
var keep: Map[string, i32] = t.1; return keep;   // exit 139
```

`t.1` is a field read on an ident, which `expr_unsafe_for` calls a borrow, so
`body_unsafe_for(t)` is false and the host looked clean. The map box leaves
through the ELEMENT, not through either name the gate was looking at. Both are
pinned in `TestSelfHostMapTupleElemHazardsX86_64`.

### The second trap: do not reach for astwalk in irlower

`tuple_pos_esc_expr` started as six lines over `astwalk.fold_stmt` with a
capturing lambda, which is the direction #6993 moved the codebase and reads far
better than a hand-written walk. It built, self-compiled, and passed every
behaviour test — and broke **five** per-unit CI gates with

```
undefined reference to `__fn_irlower__tuple_pos_borrow_only$clo'
```

A capturing lambda in call-argument position, where the call is an ASSIGNMENT's
value rather than a `return`'s, lowers to `const_func(<fn>$clo)` while
`hoist_escaping_closure` only builds `<fd>$clo` for a body ENDING in
`return <lambda>`. The whole-program build never notices; every per-unit link
does. Filed as #7215. `checker.fern:5745` uses the same generic with a
capturing lambda and is fine, because its lambda is in the return position.

So: in irlower.fern, hand-write the walk, the way the twenty-odd sibling escape
walks either side of this one already do.

`map_recv_borrows` is a whitelist for the same reason: `set`/`insert`,
`delete`/`without` and `clear`/`cleared` all hand back the receiver's own mapbox
and `iter` holds a raw pointer into it, so a map method added later refuses
until it is classified — which leaks rather than over-releases.

## Gates

`internal/e2eselfhost/self_host_map_tuple_elem_reclaim_test.go` — 8 flat cases
(census flatness at 100 vs 200 rounds, x86-64) + 10 behaviour cases, all 18 run
on x86-64, arm64 and wasm for the answer. Non-vacuity: **8 of 8** flat cases fail
against the parent commit; all 10 behaviour cases pass on both, which is the
point — the refusal set did not move.

Also green: `internal/e2eselfhost -run '(?i)(Map|Tuple|Reclaim)'` (862 s, 0
failures), the three self-host fixture legs
(`FERN_SELFHOST_FIXTURES=1`, 1134 s, 0 failures), and the per-unit family that
caught the closure trap below —
`TestSelfHostPerModuleEmitAllFixpointX86_64`,
`TestSelfHostAssumeEligibleByteIdenticalX86_64`,
`TestSelfHostWasmWholeCompilerShardedLink`.

## Instrument note: `__heap_bump_bytes()` did NOT see this leak

Worth recording because it nearly became the arm64/wasm gate. Two probes with
IDENTICAL leakcheck censuses (`allocs=2750 frees=1100 live_bytes=35200`) read
`__heap_bump_bytes()` deltas of `> 4096` and `0` — the only source difference
being an unrelated `__rc_underflow_count()` call elsewhere in `main`. The census
is exact here and the bump reading is not, so the byte gate is x86-64 only.

That is also forced: `leak_check_on` lives in `asm_ir.fern` and has no arm64 or
wasm sibling, so a self-host arm64 build emits no census at all where a NATIVE
arm64 build does. The arm64/wasm legs gate the answer and the over-release
counter instead.

## What is left, measured

1. **The clone orphan — 64 B/round, the whole residue of the headline shape.**
   `m = m.insert(k, v)` takes `lower_map_clone_insert` whenever
   `is_aliased_name(m)`, and the clone abandons the receiver's old box with no
   owner. It is NOT a tuple bug: `var q = m; m = m.insert(..)` with no tuple
   anywhere leaks the same 192 B/round, unchanged by this work. Native does not
   clone at all here — it runs `__map_set_impl` in place and gets the same answer
   (verified: `var n = m; m = m.insert("k", 7); n.get_or("k", 0)` is 1 on interp,
   native and self-host alike), so native's copy-on-write is a RUNTIME rc check
   where the self-host's is a whole-body syntactic scan. Tracked as #7235.

   Do NOT read that as "match native and the clone goes away": `__fern_map_new`
   allocates the mapbox with `__fern_alloc`, not `__fern_arr_box` — 16 raw bytes
   with NO rc header, which `__fern_map_free` returns to the freelist as such. A
   mapbox carries no refcount, so `__fern_rc_is_unique` on one has nothing to
   test and native's runtime guard is not available here. What is left is either
   a flow-sensitive `is_aliased_name` (a wrong-answer risk, not a leak risk) or
   giving the mapbox an rc header, which is a box-layout change across all five
   emitters. #7235 has both.
2. **The `(i32, Map[K, V])` tuple BOX earns no reclaim of its own** — neither
   "TUP:" (`tuple_type_is_all_scalar` says no) nor "TUPRC:" (no rc child). At
   most nestings something else frees it; in a `for` body nothing does, which is
   the 80 B/round residue of `for_host_box_residue` (208 before). The no-tuple
   control at the same nesting is flat, so the residue is the box.
3. **A fresh map declared in a match arm or a for body, in a function with no
   enclosing loop, is never swept** — pre-existing and nothing to do with
   tuples: `round(i) { match (o) { Some(v) => { var m = map_new(4); … } } }`
   leaks 64 B/round identically before and after, where the same map at the
   function's top level is flat.
4. **A map METHOD at a tuple-element position bails the whole function** (#7213):
   `var u = (m.len(), 5)` → `FERN_STRICT_IR: f (did not lower: tuple literal)`.
   `len`, `has`, `get`, `get_or` and `iter` all bail there; `keys`, `values` and
   `insert` lower. All eight lower outside a tuple, so this is
   `tuple_elem_ctor_eligible` branch (e) keying its `.len()` carve-out on
   string/array receivers only — a map receiver cannot answer the struct-typed
   registry above it. A correct fix needs `elem_type_tag` to name a map method's
   result kind (`get_or` on `Map[K, string]` is a string slot), which is its own
   change.
5. **`(i, xs)` — an ARRAY at a tuple element — leaks 40 B/round**, native flat,
   and is UNCHANGED by this work. It is the opposite mechanism and needs the
   opposite fix: construction DOES `rc_inc` an array slot, so the box holds a
   counted reference, the local's sweep only spends its own, and the interlock
   needs the element release too — #7184's shape, not this one.
