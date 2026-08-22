# The Option family, keyed on the binding

#7253 step 1 for all seven Option tags — `"OPTAARR:"`, `"OPTTUP:"`,
`"OPTSTRUCT:"`, `"OPTARRARR:"`, `"OPTARR:"`, `"OPTARRERR:"`, `"OPTSTR:"`. The
fifth family through, after tuple (#7272), `"STR:"` (#7292), `"SARR:"` /
`"ARRARR:"` (#7253, #7335) and the bare-name struct credit (#7349).

## Severity is not a property of the class

This is the finding, and it outranks the conversion. All seven tags had the same
defect — a name key handing a credited local's verdict to a same-named sibling —
and the same defect produced two completely different signals:

| class | collided | rename control | oracles |
| --- | --- | --- | --- |
| `OPTTUP:` | **99** `153/153/0` | 61 `153/150` 120 | 61 |
| `OPTSTRUCT:` | **99** `153/153/0` | 34 `153/150` 120 | 34 |
| `OPTAARR:` | **99** `600/300` 12000 | 68 `600/300` 12000 | 68 |
| `OPTARRARR:` | 68 `600/400` 8000 | 68 `600/200` 16000 | 68 |
| `OPTARR:` | 20 `300/200` 4000 | 20 `300/100` 8000 | 20 |
| `OPTSTR:` | 3 `150/100` 2000 | 3 `150/50` 4000 | 3 |

The first three fault. The last three do not, and the reason is **not** that they
are safer: those classes leak their own source box, so the stray release lands on
something nothing else claimed and no underflow fires. The census even reads
*better* — the colliding program frees MORE than its rename control.

They are the same bug waiting for its own fix. Close the leak, the source gets an
owner, and the identical stray dec becomes a double free. So a name-keyed class
that "only leaks" is a trap armed by whoever fixes the leak, and the ordering —
convert before widening, and before fixing a leak in the same class — is a
safety property rather than a scheduling preference.

**Three of the six rows get a BIGGER leak from this change**, and that is the fix
working: removing a release that was never owed exposes the leak it was masking.
Each converges exactly onto its rename control.

## `OPTAARR:` is the row to remember

Its colliding and renamed programs have **byte-identical censuses** —
`allocs=600 frees=300 live_bytes=12000` for both — and differ only in the exit
code, 99 against 68. Same allocations, same frees, same live bytes, one of them
corrupting the heap. There is no reading of `FERN_LEAKCHECK` output, at any level
of care, that separates those two runs.

## What moved

- 7 collectors: `push_str_unique(a, v.name)` → `reclaim_site_key_of(v.name, v.line, v.col)`.
- 8 credit writers in `reclaimable_names_of` (`"OPTARR:"` has two halves, and
  `"OPTARRERR:"` is a SUB-TAG appended beside the second — same slot, same key, so
  the pair cannot end up keyed differently).
- 7 predicates → one shared `opt_credit_at(s, i, pfx)`, which refuses a slot with
  no recorded site.
- 2 gate loops that read list entries take `reclaim_site_name` back out
  (`"OPTAARR:"`'s four gates, `"OPTARRERR:"`'s one). The other five loops credit
  directly and needed nothing.

**No retirement refusal to write out**, unlike the struct family. All seven
Option readers already resolved through `reclaim_slot_name`, so they were
retirement-aware; a site key is retirement-aware by construction, and the sets
coincide.

**No derived markers**, checked before starting rather than after. `"NODEEP:"` /
`"FLDCHECKED:"` are built in a loop bounded by `var mvn = out.len()` captured
before the first Option append, so that loop structurally cannot see an Option
entry. That is a proof, not a grep that came back empty — the distinction that
cost #7349 a rebuild when its equivalent readers were missed.

## The collectors needed no gate conversion, and why

All seven walk `StmtVar` and run every gate on `v.name` **before** pushing, so
only the push changes. The struct family's gates ran on accumulated list entries,
which is why that migration had to thread `reclaim_site_name` through two loops
and this one only through the two that kept the older shape.

## The predicted emit-hash hit, and whether it fired

Registered before the sweep: rows may differ where a **non-`StmtVar` binder** —
a for-in element, a match payload, a destructure key — shares a name with a
credited Option local. Five of the eight `mark_opt_type` sites are such binders;
they carry no binding site, so under the site key they lose an accidental credit
that only ever matched by collision. See the PR for what the sweep actually said.

The shape is real independently of whether the corpus contains it, and
`binder_forin_collide` is the proof. A `for o in keep` element binder, in a body
that also declares a credited `var o: Option[i32[]]`, inherited that verdict and
released elements of `keep` that `keep` still owns:

| | base | after |
| --- | --- | --- |
| `for o in keep` (collides) | `700/500` 8000 | `700/300` 16000 |
| `for e in keep` (control) | `700/300` 16000 | unchanged |

Two stray releases a round, on a binder no collector ever credited. It is the
latent form again — `keep`'s own class leaks, so nothing dissents — and it is the
clearest statement of what a name key actually does: it does not merely confuse
two declarations, it hands a verdict to any binder that happens to share a
spelling.

## Non-vacuity, and the asymmetry in it

`internal/e2eselfhost/self_host_option_credit_site_key_test.go`, 18 cases x 3
backends. Reverting `irlower.fern` fails **12** subtests:

```
opttup_collide exited 99, want 61 (99 = rc underflow: a same-named Option
local inherited another binding's reclaim credit)
optarrarr_collide: leakcheck: allocs=600 frees=400 live_bytes=8000 —
want frees=200. MORE means a binding is releasing something it does not own
```

All six collide rows fail on x86-64; only the three FAULTING rows fail on wasm
and arm64, because those legs assert exit codes and the latent three do not move
one. That asymmetry is the severity split made visible in the gate itself.

The six `credited_*` rows pass either way and are the silent-half guard: a site
key that resolves to nothing denies the credit, which no exit code would show.
All six still balance at `live_bytes 0`.
