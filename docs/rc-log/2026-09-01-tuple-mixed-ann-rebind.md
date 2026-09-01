# Compound scalar elements join the TUP class; the assign rebind gives array retains back (#7226)

The two remaining #7226 residues were labelled "assign-form tuple rebind"
and "same ident in two tuples". Only the first label was right.

## The same-ident row was a red herring

`(i, xs)` beside `(i + 1, xs)` strands 80 B/round; `(i, xs)` beside
`(j, xs)` — same sharing, bare-ident scalars — is flat. Varying one
element at a time pins it: a tuple literal with ANY compound scalar
element (`(i + 1, ys)`, `(i * 2, ys)`) got **no release at all**
(allocs=2 frees=0 on one round), while `(i, ys)` and `(7, ys)` were
clean. The sharing never mattered; the second tuple's `i + 1` did.

The refusal was `tuple_lit_is_fresh_scalar` (Number/Bool/Ident leaves
only) plus the annotation leg admitting only ALL-scalar tuple types, so
`(i32, i32[])` fell through both. The comment on the leaf test says it
plainly: "a conservative bound, not a safety requirement".

## The fix

- `tuple_ann_admits_fresh_mixed`: the binding's tuple annotation judges
  each position — a scalar-typed position may hold any expression (the
  type proves it references nothing), an rc position must stay a bare
  ident. Non-ident rc elements remain the deep TUPRC:/TUPRCS: classes'
  own; admitting one here would put a slot in both classes, which is the
  double box dec the issue's recorded negative result measured. The
  refusal IS the class boundary.
- The assign rebind `t = (k, ys)` freed the superseded box through
  `emit_arr_store`'s shallow dec and stranded the box's retained element
  (40 B/round survived even with all-ident writers). The StmtVar
  re-declaration has driven `emit_tup_elem_reclaim_store` all along; the
  assign path now takes it too, restricted to 'a' kinds — an array
  element's release is one type-fixed rc_dec whichever writer stored it,
  while a string position may hold a VIEW local from a different writer
  than the one the kinds were recorded from, and a str_free there frees
  a box the view's own sweep still owns. The string-position rebind
  therefore keeps a bounded leak, pinned by the hazard test.

`scalar_tuple_decl_ann_all_scalar` folded into the new predicate and the
TUPREBIND gate takes the annotation string instead of an all-scalar flag.

## Measured

x86-64, 200 rounds: assign rebind 800/400 live 16000 → 800/800 live 0;
two tuples over one ident 600/200 live 16000 → 600/600 live 0; the
single compound-scalar tuple 400/0 → 400/400. Hazards all balance with
zero rc underflows: element extracted (escape gate holds for widened
members), rc-literal child stays TUPRC (no double free), source read
after rebind, branch-only rebind, untaken-branch null. Answers equal
interp and native on every case, all three backends in the e2e legs.
Non-vacuous: on the parent commit all six leak-gated cases fail.

The string limb at the assign rebind is the recorded residue; closing it
needs either writer-agreement analysis over view-ness or the slot-keyed
facts #7253 proposes.
