# The copying-builtin argument credit (#7867 slice 2)

A builtin that COPIES its string argument — `strbuf_append` memcpys
the bytes past the buffer tail, `print`/`write`/`eprint` write them to
an fd, `__memchr`/`__rmemchr`/`__ascii_run`/`__count_byte` scan them —
retains nothing, and its scalar/void result cannot alias the argument.
Both halves of the credit's obligation hold, and neither analysis knew
it: the classifiers refused every builtin argument position (the
summary is keyed by defined functions), and `computeFreeEligible`
tainted any local passed to a builtin it did not know.

That one gap was two distinct leaks:

## The argument temp

`s = s.write(pfx + body)` — the `EmitState.write` shape, 573 static
sites in the self-host sources. The fresh concat is stashed only when
`countedArgTemp` fires, and `paramCountedRetain[write][1]` was false.

| shape, x86-64, non-SSO strings | before | after |
|---|---|---|
| `wr(st, pad + "…")`, `strbuf_append` callee, 100 rounds | 101 / 1, 3200 B | **101 / 101, 0** |
| the same at 200 | 201 / 1, 6400 B | **201 / 201, 0** |
| `scan(st, mk(…))`, `__count_byte` callee, 100 rounds | 202 / 102, 3200 B | **202 / 202, 0** |

The scan callee carries the state's string field into its result, so
`returnsNoParamEscape` stays false and the per-argument credit is the
only route — that is what makes the case a pin on THIS credit rather
than on the call-level gate.

## The bound local

`var msg = pfx + body; __memchr(msg, 97, 0);` — the
`computeFreeEligible` taint half, single-word x86-64 only (the
two-word ABIs never took this taint):

| 100 rounds | before | after |
|---|---|---|
| allocs / frees / live | 100 / 0 / 3200 | **100 / 100 / 0** |
| 200 rounds | 200 / 0 / 6400 | **200 / 200 / 0** |

## The table, and what keeps it sound

`copyingBuiltinArgs` is hand-audited, not derived from the inert
registry, because "inert on its arguments" is not "safe to credit":
`__method_Map_get` / `_get_or` / `_keys` / `_values` /
`MapIter_key/_value` are inert and their results alias the receiver's
interior — the result axis the registry's own header says it does not
model. Two unit tests pin the membership from both directions: every
member must be inert per the registry, and the aliasing-result names
must be absent. The checker's E006 (a builtin name cannot be
redeclared) is what lets the classifiers consult the table without a
defined-function gate.

The refusals still hold: one copying use does not launder a retaining
one (`everyOccurrenceSafe` is all-or-nothing), an `own` parameter is
skipped before any classifier runs, and `ownedByCalleeAt` suppresses
the stash at an owned-by-default position. Each is a corpus case.

## What the gates said

Six conformance fixtures improved and were re-banked:
`narrowing_cast_of_expression` 10 → 0, `wide_shift_by_variable` 5 → 0,
`string_builder` 2 → 0, `prop_string_involution` 1124 → 580,
`http_cookies` 15 → 14, `url_codec` 2 → 1. The three newly-clean ones
join the certify oracle's population, which still reports zero. The
rc-correctness corpus is green on all three backends and both leak
gates pass; `bin/fern-selfhost` grows 16,384 bytes (+0.06%), the decs.

Two of the five new corpus cases are pinned nonzero, deliberately:
the launder case leaks the temp its refused parameter strands — the
#7867 residual class the refusal exists to preserve — and the
own-string case is a pre-existing gap in the `own` machinery
(byte-identical with the credit removed, single-word x86-64 only,
clean on arm64). Both entries carry that note in the table.

## What this slice does not claim

The runtime attribution on #7867 ranks this class below the parser
node constructors: `EmitState.write` does not appear in the driver's
leaked-block table at all, because most of what flows through it is
either a literal (nothing to free) or reachable from state the driver
holds anyway. The measurable win is the two shapes above plus their
bound-local siblings; the 573-site figure is a static count, not a
byte claim.
