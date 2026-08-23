# trim copies, and the view class loses its worst member

#7393, and step 1 of `docs/STR-VIEW-CONTRACT.md`. Not a leak entry: the defect
was a deterministic wrong-answer UAF that every memory instrument read as
clean.

```fern
function mkv(): str { var s: string = mk("  pad  "); return s.trim(); }
```

| shape | interp / native | self-host x86-64 before | after |
| --- | --- | --- | --- |
| returned view + recycling alloc | 17 | **39** — `v[0]` read the recycler's `'Z'` | 17, `600/600` 0 |
| returned view, no recycler | 11 | **0** — read the freed block's zeroed byte | 11, `4/4` 0 |
| same-frame, receiver used after | 17 | 17 (correct under the view) | 17, `400/400` 0 |

Underflow 0 throughout, before AND after — the counter never saw the UAF,
because nothing double-freed: the view read memory that was *legitimately*
freed once. The census was equally blind. Only the differential answer
dissented, which is the leak matrix's exit-match rule doing the work the rc
instruments cannot.

## The fix is six characters, and then the deletions are the diff

`rt_src_str_trim` returns `s[start:end] + ""` — the owned-copy idiom the
compiler's own sources already use — matching native and interp, whose trim
always copied (the review's finding that native was safe *by not implementing
the view*). Wasm's slice op already copied; it gains a concat and keeps its
answers.

With trim owned, it joins the ordinary fresh-producer sets
(`str_fresh_alloc_method`, `str_free_producer_ident`,
`str_producer_ownership` → Owned) and the trim-SPECIFIC machinery deletes:
`init_is_str_trim`, `trim_str_init`, `collect_trim_local_names`, the box-only
credit grant, and the `str_view_local` marking for trim inits — an owned trim
is just a fresh string. The emit path gains `free_stashed_recv`, which trim
alone had to skip while its result aliased the receiver.

The alias suite's `string_alias_trim_view_partial` pin — 300/250 with a 1200-
byte view floor, "pinned as measured rather than as wished for" — CLOSES:
renamed `string_alias_trim_closes` at `400/400` live 0, now the row that fails
if the copy ever reverts.

Cost: one extra alloc per trim (the copy — native's exact cost) plus the
anonymous slice temp's box; both are freed (the repro's census balances), so
the earlier worry about a per-call view-box residue did not materialize on
these shapes.

## Left deliberately

`lines` and `split` return ARRAYS of slices — the same class through the
`string[]` machinery (#7230), entangled with the `str_arr` deep-retain
question (#7391). Their fix is the next slice, not a rider on this one. And
the bare-slice `s[a:b]` view semantics on the register backends stay: the
compiler's own sources lean on slice-as-view for performance, with `+ ""` as
the owning idiom — that is the O1-vs-O2 boundary `STR-VIEW-CONTRACT.md` leaves
to the owner.

## The pin that caught the first version, kept as the rule

The first version added trim to the state-free `str_fresh_alloc_method`, and
CI's `str-trim-user-method-not-credited` pin failed with exit 97 — value
corrupted: a USER method named `trim` on a struct receiver returns a field
alias, and a name-only admission credited it, freeing the field under its
owner. Exactly the hazard the deleted `trim_str_init` comment warned about,
proven live by the suite built to pin it.

So the shipped design admits trim's freshness only where the receiver's
STRING-ness is proven, never by name: the ownership arm gates on
`expr_is_str` (it has LowerState), the binding credit keeps the restored
receiver-type-gated trim collector, and the fresh-ret registry gains a
`trim_recv_is_string_param` arm (param annotation or a single
`var s: string` declaration — the `to_string`-on-scalar precedent one type
over). The free-fn spelling `str_trim` joins `str_free_producer_ident`, the
same exposure the rest of that family already carries.

## Non-vacuity

`internal/e2eselfhost/self_host_trim_owned_copy_test.go`, 3 cases × 3
backends. The reverted compiler fails both escaping rows by exactly the
recorded wrong answers — 39 and 0 — and the same-frame control by its census.
The container-alias suite re-pins the closed row. All string suites and the
leak matrix pass unchanged.
