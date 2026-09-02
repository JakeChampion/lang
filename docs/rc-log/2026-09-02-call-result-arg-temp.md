# A call result handed straight to another call is released after it (#6644 distcheck, slice 8)

`2026-09-02-identity-return-counted.md` ended on the argument-temp class:
of the 12,997 buffers `assign_target_into` grew on `lexer.fern`, the 12,578
survivors were each walk's final buffer, the one `body_assign_targets`
returns and its caller hands straight to `util.index_of_str`. Nothing owned
it: no slot, so no sweep; a borrowing callee, so no transfer.

## What the caller already released

The direct-call lowering stashes a fresh argument temp and frees it after the
call — a scalar literal, an "ARR:" producer's result (every return a fresh
sole-owned buffer), a consumed `.append` temp — at a BORROWABLE position
(the callee neither keeps nor returns it) or a COUNTED-RETAIN one (the
callee's store retains, so the dec nets to a single owner). The walk result
is none of those: `body_assign_targets` returns the accumulator it was
handed, which is "ARROWN:" (#7259: every return leaves the caller owning one
reference), and that registry was read by the discard, `.len()` and index
reclaims but not here.

Two more halves of the same shape:

- **the seed**: `targets(xs, [])` hands a fresh literal to a CONSUMED-THREADED
  parameter (reassigned, not `own`). The callee's ownership flag starts
  clear, so it never releases the buffer it was handed — correctly, it is a
  borrow — and neither does anyone else. Native closed this as
  `2026-09-02-consumed-array-arg-temp.md`; here `FnSigs.consumed_arr_positions`
  ("fn|idx", the predicate lower_func allocates a flag for) admits the temp,
  and the release sits behind a pointer-changed test against the call's
  result when the callee returns an array, because the callee hands the
  seed itself back when nothing rebinds the parameter. A result that cannot
  be a pointer takes no test.
- **the empty literal**: `discardable_scalar_arr_lit` refuses `[]` — it judges
  elements, and there are none — so the seed was never a temp at all. An
  empty buffer holds nothing a shallow dec could strand, whatever its
  declared element type.

And "ARROWN:" learns the forwarding return: `return targets(xs, e)` to a
function already admitted is the caller's on every path too, which is how a
`pick` over an accumulator qualifies.

The "ARROWN:" admission at an argument stays scalar-element (its return type
through `ret_type_fns`): the release here is the shallow `__fern_rc_dec`,
which for a `string[]` would give back the buffer and strand every element.
Those keep the "STRARR:" counted-position path.

## Measured

`conformance/cases/call_result_arg_temp` (the hand-back, the seed, the
forwarding producer, 300 rounds each of two shapes):

| | leaked blocks |
|---|---|
| before | 1200 |
| hand-back admitted | 900 |
| seed at a consumed position | 600 |
| empty literal admitted | 0 |

No sanitizer finding.

The sanitized self-built stage1 assembling natively (`-o`). "Before" is main
plus the identity-reclaim fix, since main alone cannot finish the compile:

| module | allocs | frees | live |
|---|---|---|---|
| lexer.fern, before | 1,782,170 | 460,574 | 317.2 MB |
| lexer.fern, after | 1,785,638 | 482,536 | 316.5 MB |
| parser.fern, before | 18,032,629 | 4,632,093 | 6.345 GB |
| parser.fern, after | 18,058,103 | 4,804,766 | 6.340 GB |

+22k frees on `lexer.fern` and +173k on `parser.fern`, for 0.7 MB and 4.6 MB
of live bytes: the temps are many and small, which is what the census said
they were. `checker.fern` exhausts the arena either way — see below.

## Main's own live-bytes regression, not this slice's

Those absolute numbers are four times what the slice before this one
measured (`lexer.fern` 75 MB, `parser.fern` 973 MB, `checker.fern` 2.69 GB,
2026-09-02-state-local-last-use-release.md). The move is entirely main's:
main plus the identity fix and nothing else reads 317 MB / 6.345 GB, and
`checker.fern` exhausts the 16 GiB arena there too.

Attributed while writing this up. A stage1 built from `c122f6f~1` — the
commit before the CFI recorder — reads `lexer.fern` 75.7 MB and
`parser.fern` 959 MB, so the recorder is the whole 241 MB / 5.4 GB. It is
not the recorder's data: `cfi_add_rule` is the "take the buffer out"
pattern that `2026-09-02-own-struct-update-reuse.md` records as one of the
eight assembler refusals —

```
var bytes: i32[] = s.rule_bytes;
s = CfiState { ...s, rule_bytes: [] };
… bytes = bytes.append(…) …
return CfiState { ...s, rule_bytes: bytes };
```

— a bare-local override, so the `own` update reuses nothing and releases
nothing, and every generation of all five rule arrays is stranded. Reduced
to 4,000 calls of that shape beside the same function written with owned
overrides only: 12,000 leaked blocks and 173 MB against 74 allocations and
74 frees. Admitting the take-out is the next slice.


## Found on the way

The sanitized self-built stage1 then faulted on every module, in
`cfi_proc_directive` — an identity reclaim releasing the field it carried,
older than this slice and reproducing on main's own compiler.
`2026-09-02-identity-reclaim-carried-field.md`.

## Pinned

`conformance/cases/call_result_arg_temp`, with a leak-census row.
