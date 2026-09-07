# 2026-09-07 — a fresh temp handed to a pair-form callee had no owner

`internal/e2e`'s `TestX86_64CertifyAgreesWithTheLeakCensus` and
`TestX86_64WriterArgTempReclaimed` both went red on `5175f5875` (#8846). They
have one commit behind them — `e46826550`, the #8405 / #8398 half that makes the
I/O helpers' per-call Option / Result box a counted rc=1 block — and two
different relationships to it.

## The certify finding was real

That commit took `multiline_stdin` and `stdin_double` from 1 unpaired
allocation to 0, which is the first time either fixture entered
`cleanCensusFixtures`. The walk then looked at them and flagged one value in
each: `__str_slice`'s result, `fresh`, still held at every `ret`.

Both fixtures are `match (line.trim().parse_int())`. `trim()` returns
`slice_unchecked(s, lo, hi)`, which `freshOwnedRcTempType` already classifies as
a fresh owned string, and it is handed straight to `parse_int` — a PAIR-FORM
callee. Every one of the four argument-temp admissions in `emitCall` carried
`!b.pairForm[id.Name]`, so nothing reclaimed it:

	function main(): i32 {
	    match (read_line()) {
	        Some(line) => { match (line.trim().parse_int()) { … } }
	        None => { … }
	    }
	}

	stdin "21\n"                    allocs=2 frees=2  live_bytes=0
	stdin "  12345678  \n"          allocs=3 frees=2  live_bytes=32

That is the whole reason the census was silent while the walk was right. On
native x86-64 a string of 7 bytes or fewer is packed inline in the value word,
so `trim()` of `"21\n"` allocates nothing; both fixtures feed short integers,
and the leak needs a result over the inline threshold to become an allocation
at all. The two-word ABIs have no inline packing, so there it is one buffer per
call at ANY length.

`Certify` observes every path and the census observes one, and this is the
shape where that difference is the finding rather than an artefact: the walk's
own note ("a function flagged in a clean fixture could in principle leak on a
path the fixture never takes") is exactly what happened, and the answer was to
fix the compiler rather than to widen the gate.

## Why the family was excluded

`resultCannotAliasArg` is the safety gate: the post-call dec fires immediately,
so it is sound only when the result cannot BE or CONTAIN the argument, which is
true of a concrete scalar and of nothing else. A pair-form callee's static type
is the ENUM — `Option[i32]` — so that predicate reads false for the whole
family, and the exclusion was written as a callee property rather than as the
result question it really is.

What a pair-form call actually returns is one tag word and one payload word.
`pairResultCannotAliasArg` asks the same question of the payloads: read off the
enum declaration after substituting the type arguments, every variant payload
must itself satisfy `resultCannotAliasArg`. `Option[i32]` qualifies;
`Option[string]` does not, and neither does an unresolved `ParamType` payload,
which keeps its prior safe-leak exactly as an unresolved scalar result does.

Only the call-level `reclaimArgTemps` is widened. The other three admissions —
`countedArgTemp`, `consumedArrayArgTemp`, `boxTempUnderPointerResult` — stay
closed to pair-form callees: the last two end in `emitArgTempDropsGuarded`,
which stashes the call's single result in a slot to run the identity test, and a
pair leaves two values on the operand stack.

## The Writer test was stale, not broken

`TestX86_64WriterArgTempReclaimed` pinned `allocs - frees == 41` — the stdout
handle plus "one write box per round … sentinel-headered and never reclaimed
(#8398)". `e46826550` reclaimed those boxes and did not revisit the test, so it
read 1 where it wanted 41 and reported it as "the build() temps are not
released" when every build() temp was in fact freed. The pin is now 1: the
per-stream handle alone, which is #8398's deliberately remaining half and does
not scale with the round count.

## Gates

`conformance-leak-census.txt` does not move: the fixtures' own paths were
already clean, which is the point above. The new rc corpus case
`pair_form_call_arg_temp_reclaimed` carries both consumer shapes — the match
scrutinee, which sets `suppressPairRebox` and leaves the bare pair on the stack,
and the `var` binding, which reboxes it first — and leaks 6400 B (x86-64) /
3200 B (arm64, wasm) without the change.
