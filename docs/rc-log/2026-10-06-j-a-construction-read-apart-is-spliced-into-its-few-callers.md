# A construction read apart is spliced into its few callers

2026-10-06: `seminline.shared_spliceable`, `sempair.reads_apart`. The serve
loop's hello request, `TestSelfHostServeAllocsPerRequest`, 3 to 1.

## The shape

```fern
match (__request_head(buf, from, limits, prev.request)) {
  Ok(head) => {
    let body = __framed_body(buf, from + head.len, head.framing, head.version, limits);
    ...
    return Framed(__framed_new(head, body.1, body.3, len));
  },
  Err(status) => { return __head_refused(status); }
}
```

`__request_head` returns `Ok(__Head { ... framing: Length(n) ... })` and is
called from the two parse entry points a server reaches; the third,
`http_parse_request_framed`, is dead there and no caller. The `Result` was paired
(sempair): position and one word, no box. The head record the word named
was a box, built in the callee for the caller to read field by field, and
the `Length(n)` framing stored in it was a second, passed whole to
`__framed_body`. A function called from one place is spliced whatever it
computes, and both boxes are read off their constructions there; one called
from two was not, so the framing probe's parse allocated nothing where a
server's allocated twice per request.

## The rule

A function called from two places (`max_shared_callers`), under the once
budget, holding no view, whose every return is a construction and one of
them a box the pair return keeps (a record, a tuple of other than two
elements, a construction holding another), is spliced into each caller that
reads the result apart, once the functions called once have folded. The
copy saves the box at that call; where a caller keeps the result whole
(returns it, passes it on, stores it) the call stays and the body remains.

Two orderings matter. The copies wait for the chains of functions called
once to fold: made earlier, they carried `__serve_loop` past the splice
budget before `__serve_event` was ready, which left the loop, `run_shutdown`
and `run` as bodies and the loop's 3-tuple as a box. A caller's readiness
ignores a shared callee whose result it keeps whole, which is never spliced
into it. A constructor leaf among the shared functions still folds its
literal calls into static boxes first (`http.ok("hello")`), and is spliced
as a shared function only at a call with a computed argument.

The rule would have spliced std/async's `run_task` into `task_start` and
`task_resume`: it returns `Suspended(Wait { ... })`, each caller matches on
it, and it had two callers. It is the trampoline, the frame a park unwinds
to, which the suspend pass knows by name (`suspend.trampoline`); spliced,
its entry call read as a park of the caller's own, and a resumed task ran
on with its frames lost (`TestSelfHostPairReturn/suspend`, three blocks
never freed, no total printed). `run_task` and `run_task_call` are
`@noinline` now, with the reason beside them.

A record read by its fields now counts as taken apart in
`sempair.reads_apart`, which seminline's split uses too; the pairing's own
candidates return variants and pairs, on which a field read does not occur,
so its decisions are unchanged.

## Measured

`TestSelfHostServeAllocsPerRequest`: 1 allocation per hello request, the
`Framed` box the loop keeps whole. `TestSelfHostSharedConstructions` covers
the two callers reading apart, the caller keeping whole and the shared
constructor leaf, on the four targets, with the pass off as well.

The compiler compiling itself: 111972408 to 111327456 allocations (-0.6%),
wall time unchanged (23.4 to 24.3 s either way, two runs each). The
self-built compiler grows from 10903008 to 11404832 bytes (+4.6%), one copy
of each shared body spliced into a second caller; the row is refreshed in
`.github/selfhost-driver-sizes.txt`. The shared budget (`max_once_insts`)
is the lever if the growth matters more than the boxes.
