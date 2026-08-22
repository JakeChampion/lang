# The CLO: family gets a field, and the 33-field pin is retired

#7253 step 2, slice 1. Not a behavior change — the emitted asm for a
capture-RC probe (array + string captures, approval and kinds paths both
exercised) is **byte-identical** before and after.

The `"CLO:"` capture-RC family — `"CLO:OK:<name>"` approvals and
`"CLO:<name>|<kinds>"` build-site kinds — lived in `reclaimable_names` for a
stated reason that is dead: the comment said *"the legacy AST x86 backend —
which still builds the bootstrap self-compile — miscompiles 34-field structs
... so LowerState must stay at 33 fields."* That backend is deleted
(#3457/#5972), the "merged bundle routes `ast`" claim with it, and LowerState
was at 31 fields. The same dead pin was cited at three more sites (the
`ret_arrdyn` flag-word folding, the `FNDYN:` seeding, the opt-fresh registry
threading); all four comments now state the current fact.

The family moves to a dedicated `clo_rc: string[]` field — same rows minus the
prefix, same three accessors, the `lower_func` seed loop deleted in favour of
constructing the field from `clo_rc_candidate_names` directly. One namespace
of the 73 gone, and the first new `LowerState` field since the pin, which is
the point: the CI fixpoint lanes compiling the 32-field struct through the
self-host backends are the standing witness that fields may grow again.

Deliberately NOT changed: the family stays name-keyed under a first-match
lookup — the collision hazard `add_tup_elem_kinds`' doc names when it explains
why tuples went slot-keyed "unlike its clo_cap_kinds model". Converting the
key is its own slice with its own probes (built to the #7253 rules: the
aliased source must outlive the callee and be released elsewhere).

Cross-match safety of the merged row shapes, since both live in one list now:
an approval row `OK:x` cannot satisfy `clo_cap_kinds_of("x")` (its byte at
`name.len()` is `K`, not `|`), and a kinds row cannot satisfy
`reclaim_has(_, "OK:", name)` (identifiers cannot contain `:`). The
empty-prefix `tagged_value_of` lookup is exact.

Found on the way, not fixed: the self-host parser rejects the zero-arg
`||` lambda form (`error[P001]: punct:||`) that `clo_rc_candidate_names`' own
doc uses in its example — the probe used `() =>` instead. Checker/parser
parity family (#7311), noted here so the next person's probe doesn't stall on
it.

Gates: the three closure capture-RC suites (x86-64 / wasm / arm64) plus the
leak matrix, 42.6 s green; the asm diff above; stage-2 fixpoint left to CI.
