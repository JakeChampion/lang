# An enum local shared into a struct field — killer-drops slice 5

`var src: E = E.A([..]); var p: P = P { e: src, … }` stranded src's box AND
its payload, once per construction (probe: 300 allocs / 100 frees over 100
rounds; native clean). The construction RETAINS the value — the k_enum
arm's dec is what that retain balances — so the strand was not the field's
claim but the source's: every rc-enum release credit refused a name that
reached a struct-literal field, because their escape walks read that
position as an escape. One inc, no dec.

## Two halves, and the second is what makes the first sound

**The carve-out.** `ef_escape_names` / `body_unsafe_for_enumfield` forgive
exactly one position — a bare ident at a struct-literal field whose
DECLARED type is an enum. Both are thin forks (the `sa_escape_names`
precedent): only the struct-literal arm differs, everything else delegates
to the strict walker, so each of its other refusals still stands. A call
argument, container element, return, lambda capture, or spread base still
escapes; none of those takes the field retain.

**The gate.** The releases those credits feed now walk the payload under
`__fern_rc_is_unique` (`emit_enum_variant_drops_gated`), which is the gate
`emit_struct_enum_field_payload_drops` has always applied from the struct
side. With BOTH owners gated, whichever releases last finds rc 1 and does
the deep drop; the other takes the shallow box dec. That holds in either
order, and neither order double-frees. Without the gate the carve-out would
be a use-after-free: the source's release would free a payload the struct's
copy still points at.

## The bug the widening uncovered

The first cut tripped the underflow guard, and the cause was a missing
conjunct rather than the new reasoning: the RCENUMS sweep — unlike the
rc-tuple sweep directly above it — never checked `moved_elided`. A
construction that MOVES the local (#6726) elides the retain and marks the
slot so the paired release is dropped too; the enum sweep swept it anyway.
Nothing reached that path while the enum credits refused every name a
construction could move, so the widening is what made it reachable. Fixed
at all four rc-enum release sites (sweep, loop rebind, and both the
top-level and nested consuming-match frees, which were separate copies —
only one of which the first cut had gated).

## What moved

`internal/e2eselfhost/self_host_enum_field_share_test.go` pins five shapes
across x86 / arm64 / wasm, both release orders included, with 99 reserved
for an over-release. Strict arm64 whole-compiler emit clean.

The construction-retain matrix's `enum__local` cell does NOT flip, and that
is not this slice falling short: the cell is COMPOUND. Its second half
(`var q: P = P { f: mkv(..) }; var p: P = q;`) is a struct alias-bind, and
that half accounts for the entire remaining leak — 300 allocs, 0 frees,
measured with the first half removed. It is the `struct__local` floor,
which is pinned leak in its own right and owns the next slice. The
enum-source half of the same cell measures 250/250 clean.
