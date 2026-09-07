# 2026-09-07 — a reassigned string parameter is the frame's, and the frame releases it

`computeConsumedParams` promoted a reassigned parameter of every pointer shape
except one: struct, tuple, enum and array were listed, `string` was not. So
`function f(a: string, s: string) { a = s + s; … }` left the frame holding a
heap buffer that nothing ever released — the exit sweep skips a borrowed param,
and the assignment's dec-on-overwrite is gated on `freeEligible`, which a
borrowed param never has. **One whole heap string leaked per call, unbounded,
with no append anywhere in the program** (#8785's parameter root).

Measured on x86-64, `FERN_LEAKCHECK=1`, 2000 calls of `f(a: string, s: string)
{ a = s + s; return a.len(); }`:

| | allocs | frees | live_bytes |
|---|---|---|---|
| before | 2000 | 0 | 64,000 |
| after | 2000 | 2000 | 0 |

The same three numbers hold for `a = a + s` and for a caller that passes a
literal rather than a local — it is the overwritten SLOT that leaks, not
anything about what was written into it.

## The accumulator the leak was hiding

`a = a + piece` in a loop, with the parameter itself as the accumulator, was
therefore not merely quadratic: it was an OOM. 8-byte pieces, x86-64 `-O`:

| N | before | after |
|---|---|---|
| 2 000 | `2000 allocs / 0 frees / 16,048,000 live` | `139 / 139 / 0` |
| 100k | OOM-killed at 53.4 s | 0.009 s, `162 / 162 / 0` |
| 200k | OOM | 0.024 s, `166 / 166 / 0` |

162 allocations for 100 000 appends is one per allocator class step: that is
`__fern_str_append` growing in place.

That is the issue's `loop_in_callee` shape written the obvious way. It was
linear only when the body opened with `var acc: string = acc0;`, because the
COPY is a local and locals were the only names the promotion reached.

## Why the entry retain, and why it is not the array's flag

An array param promoted this way carries a runtime ownership BIT instead of an
entry retain, because a retain puts the incoming buffer at rc 2 and rc 1 is
what `__fern_arr_push_grow` gates its in-place path on (#6021). A string wants
the opposite, and for a reason the array shape does not have: the incoming
buffer is the CALLER's, and `__fern_str_append` at rc 1 would grow it in place
and restamp its length — so `bump(base)` would lengthen `base`. The entry
retain's rc 2 is what sends the FIRST append of each call down the copy path.
Every later append in the body sees the replacement, which this frame built and
solely owns, at rc 1 and grows it in place. First-copy-then-in-place is exactly
the behaviour the issue measured for `loop_in_callee`, now without the leak.

So the gate that keeps it sound is a count, not an analysis, and
`str_param_append_leaves_the_callers_string_alone` in `rcCorpus` is the case
that separates the fix from the tempting wrong one: it reads `base.len()`
before and after, and every check is a LENGTH because the length prefix is what
an in-place grow moves. Remove the entry retain and it reads 23 instead of 20.
Its sibling `str_param_append_through_a_field_and_an_alias` reaches the same
admission through `b.buf` and through a second live name for one buffer.

## The two-word trap

`__fern_str_inc` returns the `(data, len)` PAIR, not a data pointer —
`returnIsString` lists it and `__fern_str_dec` deliberately beside it, which
returns only `data`. A discard-retain is therefore **two** `OpDrop`s on wasm and
arm64 and one on native single-word x86-64. With one drop the stray word shifts
every later operand-stack position in the frame, and the arm64 leg of the new
corpus cases came back with wrong VALUES (exit 4 / exit 2) while x86-64 was
green — the shape of bug that only a cross-backend corpus row catches.

The wasm half of that pair cannot be RUN on a dev box, so the gate that stands
in for it is `verifyStack` at ptrW 4, asserted inside
`TestLowerStrSelfAppendThreadsConsumedParam`: the same op list is well-formed at
one width and not the other, which is the asymmetry that pass exists for.

## The caller's half: the summary had to learn about the write

The promotion balances the CALLEE. It does not, on its own, reach the caller's
own local, and the three `rcCorpus` rows above are exactly the shape that shows
it: each still stranded ONE buffer — the string `main` passed in — no matter how
many times the loop called through.

`computeFreeEligible` refuses to reclaim a native single-word string local
passed to a user function unless `paramCountedRetain` credits that position
(#4174: a callee may retain the argument uncounted, which the intraprocedural
analysis cannot see, so the blanket taint is a leak-not-a-dangle default).
`stringParamCounted` credits a parameter only when EVERY occurrence is
positively classified, and it had no arm for the parameter as an **assignment
target** — so a reassigning callee refused by construction, and the refusal
travelled back to the caller. `bump(base, "XYZ")` on `bump(a, s) { a = a + s;
return a.len(); }` stranded `base`'s whole buffer.

Two arms close it, and the second is the one that needs the promotion:

- **the write.** A parameter slot as an assignment DESTINATION names the slot,
  not the buffer. The reference it discards is the frame's own — the entry
  retain's — so the caller's is untouched, which is the only thing this summary
  asks about.
- **the bare `return p`, but only when `p` is consumed-threaded.** Then the
  entry retain is the count move-on-return transfers to the result, and the
  sweep skips the slot. On a BORROWED parameter the same `return p` hands out
  the caller's reference with no count behind it and keeps its refusal — that
  is `handout` in `TestReassignedStringParamIsCountedRetain`, the control that
  makes the gate readable. Ungate it and only the unit test notices: the corpus
  does not currently hold a callee that returns a borrowed string param bare,
  and an over-credit there is a dangle, not a leak.

The call site needs the CALLEE's verdict, so `consumedStringParams` projects
`computeConsumedParams`' string path whole-program — a projection rather than a
duplicate for the same reason `consumedArrayParamPositions` is one
(`ownedByDefaultShape` has no `StringType` arm, so the verdict-dependent gates
cannot fire), pinned by `TestConsumedStringParamsMatchTheLoweringVerdict`.

`bump(base, "XYZ")` twice over a 160-byte `base`, x86-64, `FERN_LEAKCHECK=1`:

| | allocs | frees | live_bytes |
|---|---|---|---|
| before this file's change | 12 | 9 | 528 |
| callee half only | 12 | 11 | 176 |
| with the caller half | 12 | 12 | 0 |

528 is three buffers: one per call frame, plus the caller's. The middle row is
what the three corpus rows fail the leak gate at: 32 bytes each, one buffer,
independent of how many times the loop calls through.

## Why `computeFreeEligible` needs no arm of its own

The promotion reaches `freeEligible` through the loop's existing entry
condition — `p.Own || paramOwnedByDefault || consumedParams[p.Name]` — and the
`StringType` arm inside it has been unconditional since #8804, which found the
two-word-only spelling leaving a single-word `own` string param unreleased on
every path. A consumed-threaded string param is therefore eligible on every ABI
with nothing added here, and narrowing the arm back to
`UseTwoWordStrings || consumedParams` would re-open #8804: `g(own a: string, s:
string) { return a + s; }` over 2000 calls reads `2000 / 2000 / 0` today and
read `2000 / 1` before that fix.

## The `own` test that had to be rewritten

`TestBorrowedStringParamStillCopies` (#8804) pinned that `put(a: string, s:
string) { a = a + s; return a; }` emits no `__fern_str_append` and no string dec
— stated as "the boundary the fix must not cross — and the row of #8785 that
stays open". It is the row that closes here, and the pin was on the MECHANISM
rather than the behaviour: what must not happen is the caller's buffer being
lengthened or double-released, and the entry retain's count is what prevents
both, at run time, while the ops become exactly the `own` ones.

So it is split. `TestReassignedStringParamMatchesTheOwnShape` pins the new
shape — the same dec counts as the `own` case (1 / 1 / 1) plus the one balanced
entry retain `own` does not take — and `TestBorrowedStringParamStillCopies`
keeps its name and its assertions for a parameter the body only READS, which is
where the borrow boundary still is.

## What this does NOT reach

`acc = put(acc, piece)` — the accumulator in the CALLER, threaded through a
call — is still quadratic: 2.19 s for 100k 8-byte appends against the bare
local's 0.010 s, and 13.6 s at 200k. Neither half of that is this analysis's.
The callee is balanced now; the outstanding cost is the caller's, because
`acc = <call that mentions acc>` gives a string local no dec-on-overwrite at
all: the call result may alias the argument, so `rhsTainted` keeps `acc` out of
`freeEligible` and the string arm of `assign()` is gated on exactly that. The
Map arm beside it already solves the identical problem with an identity guard
(`new != old` → drop; `new == old` and the RHS carried a count → dec) and the
string arm has no such form. **#8836**; it is a caller-side ownership question,
not a parameter one, and it reaches every `s = f(s)` in the tree rather than
only parameters.

The other two roots of #8785 are closed: the bare local was always linear, and
the struct field `b = B { ...b, buf: b.buf + piece }` was closed by #8790 —
0.010 s at 100k, `163 / 163 / 0`.
