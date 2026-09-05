# 2026-09-04 — the in-place grow's result is uncounted, and the one release that had to be bracketed

`lower_field_append_inplace` retained its result on the IDENTITY arm — where
`__fern_arr_push` had grown the field's own buffer and handed the same pointer
back — on the reading that the struct literal the value feeds becomes a second
owner. **Nothing ever decremented that retain.** The release that would have
paid it is `__field_reclaim_<T>`'s array arm, and that arm cow-SKIPS a field
whose pointer is equal in `old` and `new` — which is exactly the shape an
in-place grow produces. So the buffer sat at rc >= 2 for the rest of its life,
the next append through the field took `__fern_arr_push`'s un-share copy, and
the buffer that copy abandoned was reclaimed by nothing. One leaked buffer per
grow, alternating with a whole-buffer copy: **quadratic** in the length of any
threaded accumulator (#8254, the cost #8224 recorded and left).

Retiring it needed the release paths audited first. The audit is below, and it
is not unanimous: one row is a real use-after-free that the retain was masking,
and closing THAT is the other half of this change.

## The audit: every release that can reach the shared buffer

After an in-place grow the buffer B has two names at rc 1 — the root
container's field `SRC.f`, and the value the append produced. The value's holder
owns it legitimately, so the question is only ever: **what can release
`SRC.f`?** `field_append_inplace_sites_of` admits a site only when the root is
the RECEIVER or a PARAMETER, never a local, which is what keeps the list short.

| # | route to `SRC.f` | what it names | who owns it there | verdict |
|---|---|---|---|---|
| 1 | `__field_reclaim_<T>(new, old, snap)` — `emit_field_reclaim_store`, the consume-rebind | `old.f`, one resolved pointer | the superseded box, replaced by `new` | SAFE. The array arm's **cow compare** skips `old.f == new.f`, which an in-place grow makes equal by construction |
| 2 | the same helper on a snapshot PARAM | `old.f` | the caller, through the entry snapshot | SAFE. cow, plus the `snap.f` compare, plus the same-box short circuit at the top of the body |
| 3 | `__fern_snapshot_dec` / `emit_arr_store`'s do_dec | the BOX | the frame being rebound | SAFE. Box-only; it never reads a field |
| 4 | `__struct_drop_<T>` — `emit_struct_field_drops` — reached through a method RECEIVER or an uncounted struct-update BASE | every rc field of a dead box, unguarded | the frame that owns the box | SAFE. `moves_fields_expr` marks both uses `"NODEEP:"`, and `emit_struct_field_drops` withholds the deep walk for such a slot |
| 5 | `__struct_drop_<T>` reached through a bare call ARGUMENT `f(a)`, which `moves_fields_expr` does NOT mark | as above | the caller's frame | **UNSAFE — see below** |
| 6 | the callee's own frame releasing its root | — | — | SAFE. The exit sweep's struct loop starts at `st.n_params`, so **no frame releases the fields of a parameter**, `own` or not |
| 7 | `__struct_arr_elems_drop_<T>` / `__fern_arrarr_free` reaching `SRC`'s box as an ELEMENT | the element boxes then the buffer | the container being deep-dropped | SAFE. A local bound from an index / field / slice read is in `grow_alias_names_of`, which withdraws its dying exemption, so the call takes the bracket and the callee's grow copies |
| 8 | `emit_struct_deep_reinit_store`'s unguarded `__struct_drop_<T>` | every rc field of the old box | the frame being rebound | SAFE by admission. It fires only for a fresh no-base struct LITERAL init, and a literal-rooted append has a LOCAL root, which the site analysis refuses |

**What closes the list** is the caller-side exemption set, which is small and
enumerable. `grow_exempt_names_of_stmt` exempts only a self-reassign
`x = f(…, x, …)` / `x = x.m(…)` and a `return f(…, x, …)` / `return x.m(…)`,
each where `x` occurs once in that statement; `grow_sole_exempt_names_of` adds a
PARAMETER read exactly once in the whole body outside any loop or lambda.
Everything else takes the #4873 bracket, which retains the caller's buffer
before the call — so `arr_push` sees rc >= 2, copies, and the identity arm is
never reached. A self-reassign is row 1; a param death is row 6; a
return-position death of a frame-owned LOCAL is row 5.

## Row 5, and why it is not an analysis question

The premise `moves_fields_expr`'s header states for a bare call argument is that
"a credited struct only ever reaches a BORROWABLE param, and a borrowable param
is read-only". The second half stopped being true when #8224 landed: a
borrowable param's FIELD can now be grown in place and handed out. And the
borrow walker does not notice, because the param occurs only as the base of a
field read, which it reads as a borrow:

```fern
function f(s: St, v: i32): St { return St { ops: s.ops.append(v), n: 1 }; }
function mk(n: i32): St {
    var a: St = St { ops: [], n: 0 };
    …
    return f(a, 999);        // return-position death: no bracket
}                            // exit sweep: __struct_drop_St(a) frees a.ops
```

`f` takes no spread, so `s` stays borrowable, so `a` keeps its reclaim credit
and its DEEP drop. `f` grows `a.ops` in place, the result holds that buffer, and
the sweep frees it — value semantics hold right up to the moment the freed block
is handed out again. Twenty appends after the call is enough: the answer goes
from 60 to 196.

The fix is at the exemption, not at the drop. `return f(a, v)` kills `a`'s
BINDING; it does not establish that this frame is done with the buffers inside
it, and the sweep runs after the call. `grow_return_local_filter` therefore
withdraws a return-position death from any name that is a frame-owned LOCAL,
leaving it for PARAMETERS — where row 6 says no frame here releases the fields
at all. That is `grow_alias_filter`'s rule read from the other side: there the
frame does not own the container, here it owns it too well.

Placing it there also covers row 4's own soft spot without naming it. That row
rests on `moves_fields_expr` marking a method receiver — except for a method the
receiver-borrow registry cleared as a pure borrow (`nomove`), which is the same
kind of proof that failed in row 5. The exemption filter does not care which
analysis cleared the drop: the only shapes where a receiver reaches a growing
callee unbracketed are the self-reassign (row 1, cow-guarded) and the
return-position death (now withdrawn for a local).

Two alternatives were rejected. Marking the call argument `"NODEEP:"` would keep
the exemption and downgrade the drop to box-only — trading a use-after-free for
a leak, which is the wrong direction for goal 2 and would strand the container's
OTHER fields as well. Making such a param non-borrowable in
`param_consumed_in_body` would withdraw the reclaim CREDIT from every local
passed to a field-growing free function, which in this compiler is most of the
threading.

## Measured

Whole-compiler emit in ONE process — `-emit asm examples/self_host/fern.fern` —
which is the workload this costs and the one the batched `-per-module-emit-all`
shape does not show. Same 4-core x86-64 container, same input tree, stage-2
binaries differing only in the lowering that built them:

| | wall | peak RSS |
|---|---|---|
| pre-#8251 (e28ccd865) | 127.4 s | 10.72 GB |
| main, with the identity retain | 118.3 s | 11.91 GB |
| **this change** | **94.8 s** | **8.32 GB** |

Against a 16 GiB arena that is 11.91 -> 8.32 GB, below the pre-#8224 figure as
well — the clone form allocated a fresh buffer per append too, it was merely
reclaimed. `grow_return_local_filter` is 5 s and 0.33 GB of that: the same
source with only the retain retired reads 89.8 s / 7.99 GB, which is the price
of the copies row 5 needs.

**The absolute numbers do not match #8254's**, which reads 155.3 s / 12.98 GB
for main and 142.1 s / 10.91 GB pre-#8251 on a different host. The RSS ratios
agree; the wall does not, and on this box main is FASTER than pre-#8251 rather
than slower. Compare within a column, not across hosts.

The per-module win #8224 measured is not given back — `checker.fern` through
each stage-2 compiler, best of three interleaved rounds:

| | wall | peak RSS | append cliff |
|---|---|---|---|
| main | 5.55 s | 1.26 GB | 270,022 crossings / 631.8 MB |
| this change | 4.73 s | 0.87 GB | 242,460 crossings / 360.4 MB |

Whole-run Ir under callgrind on the same pair: 18,116,237,097 ->
17,911,371,721. Per-SYMBOL shares are not quoted: valgrind does not read the
self-host `-g` symtab and an `nm -n` resolution left 79-83% of the run
unattributed, so `__fern_arr_slice`'s 6.03% is not re-derivable with that
instrument here. The cliff bytes above are the reading that does hold, and
`TestSelfHostFieldAppendInPlaceShapeX86_64` is what pins that the in-place
decision still fires at all.

The allocation shape read directly with `__heap_bump_bytes()` over a threaded
2000-append accumulator: **10.8 MB with the retain, under 64 KB without** —
quadratic against linear, which is the whole of the whole-compiler figure above.

## Pins

`TestSelfHostFieldAppendInPlaceReclaimsX86_64` reads that allocation off the
running program: the admitted shape must bump <= 4 x 64 KiB (it bumps 0; it
bumped 165 at the parent) and the analysis-REFUSED shape beside it must bump
>= 32 (it bumps 200, clamped, either way). The refused case is the calibration —
without it a passing admitted case would be evidence about the instrument rather
than about reclaim.

`return-position-death-frees-the-grown-buffer` is row 5, in the shared
three-backend table: it exits 196 against the oracle's 60 with the retain gone
and `grow_return_local_filter` stubbed out, and it is the only case in the table
that does. Three more cases pin audit rows that were already sound —
`return-position-death` (the spread spelling of row 5), `own-param-container`
(row 6), `struct-array-field` (row 1's element walk). Those three pass at the
parent too: they record what the audit established, they do not reproduce a bug.

## Traps

**The instrument wraps.** `__heap_bump_bytes()` returns an i64 and an exit status
carries 8 bits, so a program returning bytes / 4096 reports 149, 80, 54, 194 for
1000 / 2000 / 4000 / 8000 appends — a non-monotonic sequence that reads like
noise and is a growing one wrapping twice. Clamp before narrowing (the gate
returns `min(bytes / 65536, 200)`), or a quadratic leak looks like measurement
error.

**The bisection in `2026-09-04-field-append-in-place.md` said "the identity arm
is not the one that crashes", and it was right.** That table was taken before
the bracket's release side was fixed to name a value rather than re-evaluate a
place; every row in it that segfaults is that bug, and the row where an
unconditional retain "fixed" it only did so by forcing every append onto the
copy path. It is not evidence that the retain was load-bearing.

**Row 5 is invisible to an answer until the freelist lines up.** The first four
probes of that exact shape all agreed with the oracle; it took twenty appends
after the call, sized to reuse the freed block, to move the answer. A shape
audit that stops at "the program printed the right number" would have shipped
it.

## Superseded at merge: the move-out closes row 5 without the copy

#8274 landed on main while this was open. Its identity arm STORES NULL into the
source field, so a return-position death of a frame-owned local (row 5) finds
nothing to free at the exit sweep, with no bracket and no copy. That made
`grow_return_local_filter` redundant, and it was not free: it moved
`TestSelfHostGrowSoleOccurrenceX86_64/K_two_calls_via_local` from its
deliberately pinned 44 copies to 50 (`return f(t, v + 1)` on a local is the
exact shape it withdraws), on top of the 5 s / 0.33 GB measured above. The
filter is gone; `return-position-death-frees-the-grown-buffer` stays as the pin
on row 5 and passes on the move alone.
