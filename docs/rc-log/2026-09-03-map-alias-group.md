# A map's aliases form a group that owns its boxes (#7235)

`m = m.insert(k, v)` on an aliased map takes `lower_map_clone_insert`, and the
receiver's old box was left with no owner — the largest self-host-only rc gap
measured, and the one #7235 said had no "match native" route: a mapbox is 16
raw bytes from `__fern_alloc`, so there is no rc word for a runtime
copy-on-write to test.

## Measured

x86-64, `FERN_LEAKCHECK=1`, 100 rounds (200 where marked), allocs/frees and
live bytes. Every answer agreed on `bin/fern -interp`, native x86-64 and the
self-host before and after; the counts are the self-host's.

| shape | native | before | after |
|---|---|---|---|
| `var q = m; m = m.insert("k", i)` (the repro) | 400/400, 0 | 1200/600, 19200 | 1200/1200, 0 |
| the same at 200 rounds | 800/800, 0 | 2400/1200, 38400 | 2400/2400, 0 |
| `m = m.insert(..); var q = m` (alias after) | 200/200, 0 | 1200/600, 19200 | 700/700, 0 |
| `var q = m; if (..) { m = m.insert(..) }` | 300/300, 0 | 850/400, 12800 | 850/850, 0 |
| `var q = m; while (k < 3) { m = m.insert(..) }` | 400/400, 0 | 2600/1400, 44800 | 2600/2600, 0 |
| two aliases around two inserts | 600/600, 0 | 2000/1100, 32000 | 2000/2000, 0 |
| `(i, m)` tuple + insert (#7212's residue) | 500/500, 0 | 1300/1000, 6400 | 1300/1300, 0 |
| `Map[string, string]`, alias + insert | 500/500, 0 | 1300/600, 22400 | 1300/1200, 3200 |
| the same with NO alias (control) | 300/300, 0 | 700/600, 3200 | 700/600, 3200 |
| fresh string keys, insert / alias / insert | 900/900, 0 | 2300/900, 48000 | 1800/1300, 16000 |
| the same with NO alias (control) | 700/700, 0 | 1100/600, 16000 | 1100/600, 16000 |
| never aliased (control) | 200/200, 0 | 600/600, 0 | 600/600, 0 |
| alias declared inside a loop (refused) | 600/600, 0 | 2000/1100, 32000 | unchanged |
| `var q = m; var r = q` (refused) | 400/400, 0 | 1300/700, 19200 | unchanged |
| the loop-snapshot program (answer 6) | 6 | 6 | 6 |

The string row's residue is its no-alias control's, byte for byte: the value
string of a `Map[string, string]` built by `m = m.insert("k", i.to_string())`
leaks 32 B/round with no alias anywhere (and 160 B/round with fresh keys as
well), so it is a string-column gap this change does not reach. The alias
group's own part of that row closes.

## The ownership argument

A mapbox has no rc word, so `var q: Map[..] = m` is an UNCOUNTED share and
the release of every box a set of names reaches has to be decided by the
compiler. Before, the analysis decided it by refusing: an aliased map lost
its "MAP:" credit outright, nothing freed either box, and the clone made a
third box nobody was accounting for.

The group: `m`, its plain aliases `var q = m` and the local tuples holding it
at an element (#7212's override) — credited `MAPAL:<qsite>|<msite>` and
`MAPTUP:<tsite>|<k>|<msite>` beside m's `MAP:`. The group holds one box until
a clone, after which the holders bound before the clone keep the OLD box and
m the clone. Which holder holds which box is a runtime fact — the clone can
be conditional, or run three times in a loop — so the release protocol is an
IDENTITY guard rather than a static assignment:

- a holder frees its box only when no holder released after it still holds
  the same pointer (`emit_map_release_unless_held`);
- the clone site frees the superseded box only when no holder at all does;
- m is released last and unconditionally.

Each distinct box is then freed exactly once, by the last holder in the
release order that still names it. The order is: tuple elements (at the top
of the exit sweep, before the array sweep frees the tuple box they are read
through), then the plain aliases in slot order, then m.

Column deep-releases stay on m alone. A clone copies the column POINTERS
uncounted, so an element two boxes share must be freed by exactly one of
them: m's sweep takes `MAPVS:` / `MAPKS:` / `MAPVA:` as before, every other
release in the group — a holder's, the clone site's — is the shallow free.
An element only a superseded box still names (a value the clone overwrote)
leaks one level; its runtime twin (retain on copy, release on overwrite in
`__fern_map_set`) is the next lead for the column classes.

Two admission rules keep the guard complete, and both are about slot
EXISTENCE at emit time rather than liveness. Every holder's slot must exist
when a release is emitted, so neither m nor a plain alias may be declared
inside a loop body: its re-declaration would run a guard the holders declared
after it are absent from, and a stale holder still naming the box would free
it again at its own rebind (a double free, not a leak — `alias_declared_in_
loop_refused` pins the refusal). And a name bound at two sites makes the
rows name-ambiguous, so m must be bound exactly once. A tuple host keeps the
override's own terms and may sit in a loop: bound after the insert, it holds
either the current box or a box only its superseded tuple names.

The clone site can be reached while a holder's slot does not exist yet only
when that holder is bound AFTER the insert in lowering order and outside any
loop — and then it is not in scope at the insert, so nothing that site could
read names the box through it.

## The clone decision, flow-sensitive

`is_aliased_name` was a whole-body scan, so `m = m.insert(..); var q = m`
cloned for an alias that did not exist yet. `map_clone_insert_sites_of` now
decides per insert: it clones when an alias of m is bound earlier in lowering
order, anywhere inside a loop enclosing the insert, or inside a defer. The
loop rule is the issue's 2026-09-02 comment made code: the back edge carries
an alias bound textually after the insert into the next iteration, so the
snapshot program (`m = m.insert(..); var alias = m; seen = seen.append(alias)`
in a `while`) still clones every iteration and answers 6, where a scan that
stopped at the current statement answers 12. That program is a row of the
gate on all three backends.

Array `.with` keeps the whole-body `is_aliased_name`; only the map self-
reassign consults `map_clone_sites`.

## Gates

`internal/e2eselfhost/self_host_map_alias_group_test.go`: 13 rows, x86-64
leakcheck exact counts with `__rc_underflow_count()` folded into the exit
and a second build of every row under `FERN_SANITIZE=1`, plus wasm-IR and
arm64-IR exit legs — 42 subtests. Non-vacuity against the parent commit
(`git checkout HEAD~1 -- examples/self_host/irlower.fern`, same test): the
9 rows the change reaches fail there at the "before" counts above, and the
4 that must not move — the snapshot program, the control and the two
refusals — pass on both sides, so the refusal set did not move.

Also green: `TestSelfHostMap*`, `TestSelfHostContainerAliasBind*`,
`TestSelfHostRcPlanDiff`, `TestSelfHostLeakMatrix*`, the x86-64 per-module
fixpoint, `TestFernFixturesSelfHostX86_64`, `internal/lint`, and the feature
census (see the PR for exit codes).

## What is left

1. **A holder declared inside a loop body** (`while (..) { var q = m; m =
   m.insert(..); .. }`) — refused, 320 B/round. Admitting it needs every
   holder's slot to exist before the first release is emitted: pre-allocating
   the group's slots at function entry and having the alias's `var` claim its
   pre-allocated slot, which is a change to `bind_var_slot`'s slot
   discipline, not to the guard.
2. **Alias chains** (`var q = m; var r = q`) — refused, 192 B/round. The
   chain machinery the string and string[] classes have (#7750) would admit
   it under the same guard; the rows are already keyed by site.
3. **The string-column residue** above — a `Map[string, string]` whose value
   is `i.to_string()` leaks the value with no alias at all. Not a group
   problem; measure the `MAPVS:` freshness predicate on that producer first.
4. **A map declared inside a loop with a plain alias** — refused with (1);
   the reinit at its re-declaration is the release that would need the guard.
