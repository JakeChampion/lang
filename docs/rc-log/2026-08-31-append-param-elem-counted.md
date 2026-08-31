# A struct element a callee appends is owned by the array, not the caller

*2026-08-31* — self-host only; both oracles read `012` on every row.

## The defect

`emitf(s, o) { return St { ops: s.ops.append(o) }; }` — the shape the self-host's
own LowerState / EmitState threading is built from — stored the caller's box into
the array **without retaining it**. The caller then released it on the binding's
next rebind, and the array was left pointing at freed memory.

It surfaced as a WRONG ANSWER, and every counter read healthy while it did.
Reading the elements back after three appends (`t = t * 10 + st.ops[k].a`):

| inner statement | answer | allocs/frees | live |
|---|---|---|---|
| a named local, appended by a CALLEE | **212** | 1800 / 1800 | **0** |
| the same, elements never read (`.len()` only) | correct | 1800 / 1800 | 0 |
| the append written INLINE in the caller | 012 | 1800 / 1404 | 19008 |

The middle row is why nothing caught it: with only a `.len()` read the dangling
element is never dereferenced. The bottom row is correct only because it LEAKS —
nothing is recycled into the dead box.

Whether the callee is a method or a free function makes no difference, and
neither does how the local was bound (a producer call or a struct literal).

## The threshold is the freelist, not a capacity boundary

One and two appends answer correctly; three is the first wrong one. Not a grow
boundary — simply the first iteration with a freed box available to recycle.
`212` names the mechanism exactly: elements 0 and 2 read the same value, because
iteration 3's allocation landed on the box element 0 still pointed at.

`FERN_SELFHOST_NO_REUSE=1` reads `212` as well, so the reuse layer is not
involved. Plain freelist recycling is enough.

## Read off the emitted code

`main`'s loop emits the cow-guarded rebind reclaim — `emit_arr_store` with
`do_dec` — before each `mkop`, releasing the previous `o`. There is no
`__struct_drop_Op` and the only `__struct_drop_St` is the exit sweep, after the
read: the premature free is that rebind dec and nothing else. (`__fern_rc_dec`
lowers to `__fn___fern_arr_dec` on this backend, which is why grepping the asm
for `rc_dec` finds nothing.)

The caller is right to emit it. Counting retains in each callee settles which
side is wrong:

| callee | `rc_inc` in its body |
|---|---|
| `holdf` — `Holder { last: o, n: h.n + 1 }` | **1** |
| `emitf` — `St { ops: s.ops.append(o) }` | **0** |

A struct-literal field store takes the construction-side counted retain; the
array append takes none. In one program exercising both, `st.ops[0].a * 10 +
h.last.a` reads **12** where the oracles read **2** — the field half correct and
the array half corrupted, side by side.

## The fix, in the two halves a counted store always takes

`lower_arr_append_value` retains a struct element that is a borrowed PARAMETER —
the enum arm directly above it has done exactly this since #6049 — and
`param_counted_of` grows a `"PCNT:"` tier so a caller handing over a fresh temp
emits the matching post-call release (`stash_fresh_struct_arg` admits the counted
position alongside the borrowable one).

**Either half alone is wrong, and measurably so.** The retain with no credit
leaves the temp unreleased: the three-append probe went 14/14 to 14/11. The
credit with no retain releases a box nobody counted.

A LOCAL is excluded from the retain, and that exclusion is the whole precision of
the rule: a local that escapes into a container has already lost its reclaim
credit to the escape walk, so the container holds the only reference and
retaining there leaks rather than balances. Restricting the retain to parameters
is what took the inline row back from 14/11 to 14/14. An `own` parameter is
excluded from the other side — the caller transferred its reference rather than
keeping one. The checker refuses a borrowed local at an owned position outright
(E051), so a fresh argument is the only way to reach that arm at all.

## What it costs, measured against main

On the outer-loop shape — a `St { ops: Op[] }` rebuilt each round, four appends
inside — every row now answers correctly, and every row leaks what the temp row
already leaked:

| row | main | after |
|---|---|---|
| temp, all four variants | correct, 19008 live | correct, 19008 live |
| local, `.len()` read only | correct, **0 live** | correct, 19008 live |
| local, elements read | **176 — wrong**, 0 live | **19 — correct**, 19008 live |

The `local` rows' "clean" reading on main is the use-after-free wearing a
disguise: the counters balanced *because* the box was freed early. The leak that
replaces it is not new in kind or magnitude — it is the same class the temp row
already carried, and the one `docs/rc-log/2026-08-31-fresh-struct-call-argument.md`
records as still open.

The gate that would catch a real leak regression does not move:
`TestConformanceLeakCensusX86_64` passes unchanged at 453 fixtures / 130 leaking
/ 45670 unpaired allocs, and the certifier oracle gate still reports zero.
