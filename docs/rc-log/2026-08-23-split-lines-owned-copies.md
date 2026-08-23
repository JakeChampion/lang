# split and lines segments become owned copies, and the SARRB view routing dies

#7230 — the array half of the view-escape class, fixed with #7393's idiom the
day after trim. Pre-fix, a split or lines result returned across its frame
answered **36** on self-host x86-64 against the oracles' 29 on the same
recycler probe shape — a segment view reading the recycler's bytes — with
underflow 0, census "clean", and no diagnostic. Both producers, same number,
same mechanism.

`rt_src_str_split` (three segment appends: char-split, mid, tail) and
`rt_src_str_lines` (two) now append `slice + ""`. With the segments owned, the
SARRB view routing COLLAPSES: `strarr_free_helper` / `strarr_elem_free_helper`
return the rc-aware full frees unconditionally, because the view-aware
siblings — which free element boxes alone — would strand every owned
element's data buffer. `__fern_str_arr_view_free` loses its last router, so
its runtime emission self-gates off through the needs system. The
`strarr_builtin` slot flag survives with exactly one job: the SARRB credit's
binding-site type confirmation (`init_is_builtin_strarr_producer` already
gates on `expr_is_str`, so the user-method hazard that bit trim's first
version has no opening here).

The ESCAPING result itself stays uncredited — the non-escape gate refuses a
returned array — so those shapes now leak soundly (`1200/600`, 128 B/round on
the probe) instead of dangling. The suite pins that number as a refusal-leak
row: more frees there must come from a credit that proves the escape, never
from re-viewing the segments. The same-frame shape sweeps fully
(`1000/1000`, live 0) through the collapsed routing.

Wasm's slice op already copied, which means wasm's split elements were
ALREADY owned — and were being released by the view-aware route. After the
collapse all three backends run one semantics through one release.

## The needs-graph dep CI caught, and why no local probe could

The trim commit's first CI round also failed `TestSelfHostStringArm64/trim-len`
with a LINK error: `__fn___fern_str_trim` had an undefined reference to
`__fn___fern_str_concat`. The owned-copy helpers now concat, but their
`mark_str_*` need registrations did not pull `str_concat` — and every local
probe built its strings through a user concat producer, so the concat helper
was always independently needed and the gap was structurally invisible from
here. The failing shape is a program that trims (or splits, or lines) and
never concats itself. All three marks now carry the `str_concat` dep, and the
no-concat shapes link and run on both register backends.

Gates: the new `self_host_split_lines_owned_copy_test.go` (3 cases × 3
backends, wants oracle-confirmed, counts harness-measured; the pre-fix
compiler answers 36 on both escaping rows), and every existing split / strarr
/ SARRB / elem-reclaim / opt-strarr suite plus `StrOwnDump` and the leak
matrix pass UNCHANGED — the view pins there were answer-shaped, and the
answers held.
