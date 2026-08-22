# The struct producer that returns a local, credited

#7343, and the first thing built on a fully site-keyed credit table.

```fern
function mk(): P { var p: P = P { xs: [1,2,3], s: w("p") }; return p; }
function round(): i32 { var v: P = mk(); return v.xs.len(); }
```

| rounds | 200 | 400 | 800 |
| --- | --- | --- | --- |
| self-host, before | `800/0` **25600** | `1600/0` **51200** | `3200/0` **102400** |
| after | `800/800` **0** | — | — |
| native / interp | `400/400` **0** | — | — |

`frees=0`: nothing was released — not the struct box, not its buffer, not its
string. 128 B/round, unbounded. The byte-identical producer that returns the
literal DIRECTLY was already clean, so one extra statement in the callee was the
entire difference.

## The issue named the wrong predicate, and the right one needed no signature change

#7343 blames `fresh_struct_ret_fns_of` / `is_fresh_struct_ret_value` and
concludes the fix "needs plumbing it does not currently have: `stmts` and the
statement index. That is a signature change rather than a two-line edit."

That is the LOOSE registry, and its own doc says it is *"consumed only by
`snapshot_local_names_of"`* — the reassigned-builder path, not the caller's
binding. What actually gates `var v: P = mk()` is `collect_fresh_ret_call_names`
reading `return_fresh_struct_ret_fns`, built through `fresh_struct_fwd_fixpoint`
→ **`return_value_is_strictfresh_struct`** — which already receives `fnbody`,
`fnparams`, `arr_fresh`, `fwd` and `sfok`. Everything the proof wants was in
scope; what was missing was an `ExprIdent` arm.

## Why it waited for the keying

Applied to the name-keyed table this exact widening **SIGSEGV'd** — exit 139, not
an rc counter — on two same-named `v`, one from the producer and one aliasing a
parameter: the alias inherited the newly granted credit and freed the caller's
box. That is what #7349 was for, and the ordering claim is now verified rather
than assumed:

| probe | name-keyed + widening | site-keyed, no widening | site-keyed + widening |
| --- | --- | --- | --- |
| `sibling` | **139 (SIGSEGV)** | 1 `204/0` 6520 | 1 `204/200` 120 |
| `sibling_rename` | 1 `204/200` 120 | 1 `204/0` 6520 | 1 `204/200` 120 |

The keying was necessary AND sufficient: after it, the colliding program and its
rename control measure identically.

## The proof, and the gate that was the wrong one to reuse

`struct_ret_local_is_frame_fresh` mirrors `arr_field_ident_is_frame_built` one
level up — exactly one declaration, not a parameter, never reassigned, an init
that is itself strict-fresh (recursively; Fern's declare-before-use makes the
chain terminate), and then an escape gate.

The first attempt reused `body_unsafe_for_allow_structret` and **did nothing at
all** — every measurement unchanged. That gate forgives the name only at a FIELD
of a returned struct literal (`return P { xs: p }`), so a bare `return p` still
reads as an escape there. Correct for its caller, which asks about an array field
moved INTO a returned struct; wrong for a local that IS the returned struct. The
fix is a `bare_ret_ok` flag threaded through the pair, so there is one gate with
two entry points rather than a near-copy.

## One row moved that I did not intend, and it stays moved

`var stolen: i32[] = p.xs;` before `return p` went from `200/0` (8000) to
`200/200` (0). A field read takes a second reference to the buffer the caller's
deep drop will free — but `stolen` is a local of the producer's own frame and
dies at its exit, so the drop is balanced: underflow 0, both oracles agreeing.

Two attempts to refuse it failed, and that is the useful part. **Neither
`moves_fields_stmts` nor `optstruct_body_moves_field` reports this shape** — a
bare field READ is precisely the blind spot #7259 records. Manufacturing a third
ad-hoc check to exclude a shape that measures correct would be a special case
shielding nothing, so it is admitted deliberately, pinned as
`field_read_admitted`, and named as the first suspect if this class ever
over-releases.

Both field-move checks stay, and `refused_receiver_field_move` is what proves
they are live rather than decorative: a method call that moves a field into its
result (`P { xs: p.xs }`) keeps the producer un-credited, `300/0` before and
after.

## What is still refused

| shape | why | after |
| --- | --- | --- |
| `return p` where p is a PARAMETER | the caller owns the box — the `return self` shape | 8000, unchanged |
| a second binding aliases the local first | two references leave the frame | 8000, unchanged |
| the local is reassigned | the declaration's init is not the whole story | 16000, unchanged |
| two declarations of the name | the init witness could be a shadowed sibling | 8000, unchanged |
| a receiver call moves a field out | the returned box no longer sole-owns it | 12000, unchanged |

## Two status-quo pins the sweep caught

Neither was a regression, and the broad sweep is what separated them from one.

**`producer_local_still_refused`** is the row #7349 wrote to prove its own re-key
widened nothing, explicitly labelled that fix's fails-before case. It now
balances at `400/400`, so its name would be a lie — renamed
`producer_local_now_credited`, which is the same rename the log records for
`string-concat-temps-still-leak`. It still earns its place one direction over: it
is now the row that fails if the credit is ever withdrawn.

**`returned_from_inside_the_block`** in the block-scoped struct-box hazards suite
pinned `wantFrees: 0`, and moved to 200. This is the row the widening's failure
mode would look like — a caller gaining a reclaim it did not earn — so it was
checked rather than adjusted:

| | exit | leakcheck |
| --- | --- | --- |
| interp / native | 6 | `800/800` **0** |
| self-host before | 6 | `800/0` **35200** |
| self-host after | 6 | `800/200` **26400** |

`__rc_underflow_count()` is 0, `-sanitize` reports `leak 26400 bytes in 600
blocks` and no rc over-release, and the count moved **towards** the oracle rather
than past it. `build` returns a loop-local whose box is fresh and sole-owned, so
the caller may release it; the residual 26400 is the instances from the
iterations that do not return, which nothing sweeps and which is not this row.

That suite's own header anticipated exactly this: *"free counts are exact and
pinned at what this build produces … the box free legitimately moves some counts
and only the counter distinguishes a new safe release from one landing on a live
box."*

## Non-vacuity

`internal/e2eselfhost/self_host_struct_producer_local_test.go`, 12 cases x 3
backends. Reverting `irlower.fern` fails **5** — the five admitted rows, all on
the x86-64 leg, because these are leaks and a leak moves no exit code:

```
producer_returns_local: leakcheck: allocs=800 frees=0 live_bytes=25600 —
want frees=800. FEWER on an admitted row means the credit stopped resolving
```

The seven refusals and controls pass either way, which is what a "fix" that
worked by widening indiscriminately would fail.
