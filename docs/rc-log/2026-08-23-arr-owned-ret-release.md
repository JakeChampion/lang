# The array a callee hands over, released by the caller

#7259's remaining two defects, closed together — the pair
`docs/rc-log/2026-08-22-arr-fresh-ret-method.md` measured and left, saying they
had to land in one change because each alone reads identically to no fix.

Loop-resident probe (the struct built INSIDE the loop, so the leak is per round),
x86-64 self-host, `FERN_LEAKCHECK=1`, native and interp as oracles:

| rounds | 100 | 200 | 400 |
| --- | --- | --- | --- |
| `keep.get().len()`, `xs: i32[]`, before | **4800** | **9600** | **19200** |
| after | 0 | 0 | 0 |
| native / interp, every row | 0 | 0 | 0 |

Exactly 48 B/round, 2.0x per doubling, `allocs=200/400/800 frees=100/200/400`
before and `frees == allocs` after. Exit codes agreed across all three engines
throughout, so this was purely a leak.

The same 48 B/round on the discarded statement, the index read, and a bare
borrowed param (`id(s).len()`), and 112 B/round on a two-field struct — 32 / 28 /
72 for the same rows on wasm, 48 / 44 / 112 on arm64.

## Two defects, and why neither moves the number alone

`return h.xs` retains the buffer on the way out (the return-transfer dup, #7232 /
#4357). So the caller owns a reference. Then:

1. **Nothing released it** where the result was consumed in place. A bound result
   pays the dec from its slot's exit sweep; `keep.get().len()`, `grab(keep)[0]`
   and a discarded `keep.get();` have no slot.
2. **The receiver lost its deep drop.** `moves_fields_expr` marks every method
   receiver a field-move hazard, so `keep.get()` earned `keep` a `"NODEEP:"`
   marker and its reclaim degraded to a shallow `__fern_arr_dec` of the box.

rc(xs) after each, on the one-call shape: today 1, release-only 1, deep-drop-only
1, both **0**. The arithmetic in the issue thread was right.

## The registry: "ARROWN:"

`fn_returns_owned_arr` — every return leaves the caller owning one reference —
rides `return_fresh_struct_ret_fns` beside `"ARR:"` under an `"ARROWN:"` prefix,
and the three in-place consumers (`.len()` receiver, `mk()[i]` read, discarded
statement) release it with the same shallow rc-guarded `__fern_rc_dec` they
already emit for `"ARR:"`. Three admitted shapes:

| return | mechanism | element kinds |
| --- | --- | --- |
| `h.xs` (rc-array field of the receiver / a struct param) | retained | scalar, struct[], enum[], string[] |
| `a` (a bare BORROWED array param) | retained | same |
| `[x, y]` | moved | scalar only |

The split is the whole design: a *retained* alias is safe to release shallow at
any element kind, because the elements belong to whoever the callee borrowed
from. A *moved* literal brings its elements with it, so a shallow dec would
strand them for a pointer element type — hence the scalar-only gate, and hence
this cannot be folded into `"STRARR:"`, whose deep free needs a fresh buffer.

Mixed bodies are admitted (`pick()` returning `h.xs` on one path and `[7, 8]` on
the other, 44 B/round before), because both mechanisms leave the caller owning
exactly one reference. All-or-nothing at the function: one path that hands back a
borrow refuses the whole entry, since the consumers release unconditionally.

## `own` is refused, and the refusal is measured

`function id(own a: i32[]): i32[] { return a; }` moves the caller's own reference
back out, so on a valid program the caller owns one and a release is right — it
leaks 48 B/round today. Admitting it took `id(s)` over a *live local* straight to
`__rc_underflow() != 0`: two names for one reference, two decs. Native rejects
that call with E051; **the self-host checker has no E051**, so it lowers the
program and would have emitted the over-release. The shape stays out until that
parity gap closes; the test pins the safety property (answer + underflow) rather
than the byte count, so it does not pin the leak.

## The NODEEP exemption

`fieldmove_stmt`'s return arm no longer counts `return name.<rc-array field>` as
a move. It cannot dangle: the return path retains, and every arm that reclaims
such a field is rc-guarded — `__fern_arr_dec` and `__fern_str_arr_free` both dec
a shared buffer instead of freeing it.

The marker is whole-LOCAL, which is what made it expensive: on
`struct H2 { xs: i32[], ys: i32[] }` with a method returning only `xs`, `ys` is
never named by the method, never aliased, and leaked all the same — 112 = 48 + 64,
both buffers. One field-returning method cost the struct every rc field it had.

## Still open: the `string[]` field limb is a different bug

The issue's headline probe uses `xs: string[]` and is **unchanged** by this — 288
B/round at 100 / 200 / 400 rounds (28800 / 57600 / 115200), native flat at zero.
The control that separates it: declare
`function (h: Holder) get(): string[] { return h.xs; }`, **never call it**, never
read the field, just build the struct in the loop — 288 B/round. Delete the
declaration and read `keep.xs.len()` 100 times instead — clean.

So it is not a call-position defect at all. `strfld_reclaim_ok_types_of`'s
`strarrfld_scan` refuses a type program-wide when any `string[]` field is read
outside a bare `.len()` borrow, and `return h.xs` is such a read; the deep drop
is then withheld for the whole type and the reader need never run. Closing it
means following the forwarded field through call sites — an interprocedural
widening of the credit whose documented failure mode is a gen1 self-compile
segfault. Filed as #7417.

## Gates

`internal/e2eselfhost/self_host_arr_owned_ret_release_test.go`, nine cases on
x86-64 / arm64 / wasm. Non-vacuity: **21 of 27 sub-tests fail** on the parent
commit, at 48 | 48 | 32 B/round for the single-field rows and 112 | 112 | 72 for
the two-field one. The six that pass on both sides are the two controls, which is
what a control is for.
