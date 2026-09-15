# A field append grows in place on the semantic path

#9365, the second half of #8785. The self-host compiler built through the
semantic lowering (`FERN_SEM_IR=1`) peaked at 11.5 GB on `lexer.fern` against
the AST build's 134 MB, and `FERN_CLIFF_REPORT=1` on a 200-function input put
the whole of it in one place: 5.4 GB copied by appends onto buffers that still
had spare capacity, every sample in the x86 GAS assembler's `x86_gas_mem_op`,
whose `code: i32[]` is the machine-code buffer and grows to the size of the
program.

The shape is the functional update through a record field:

```fern
struct R { ops: i32[], n: i32 }
function emitop(r: R, op: i32): R { return R { ...r, ops: r.ops.append(op) }; }
```

which semsource produces as

```
v0 = param 0             (r: R)
v2 = record_get(v0, ops)
v3 = append(v2, v1)
v4 = record_get(v0, n)
v5 = record_new(v3, v4)
```

`v2` is a projection, so the plan supplies the append's receiver by RETAINING
it, `sole_owned_base` then reads a count of 2 on a buffer only the record
holds, and every push copies. 40,000 pushes: 11 s and 8.9 GB on x86-64, where
the AST lowering takes 60 ms — through an `own` record exactly as through a
borrowed one, since the field's count is the record's, not the parameter's.

## Why the parameter fix did not extend

#9398's deferral holds back the retain of a receiver that IS a borrowed
parameter, because the caller-side bracket of #4873 stands behind exactly that
value: a caller still reading the array holds a second count across the call.
A record argument is not bracketed at all, and bracketing its box would answer
for the wrong box — the buffer the push tests is the field's, at its own count.

What makes the AST lowering's `lower_field_append_inplace` sound is
`fai_admit_stmt`'s whole-function argument: the record is not aliased, not
captured, and not read after the site except through other fields. That is a
real analysis, and on this path it is a liveness question over the value graph
rather than a walk over spellings.

## What landed

**Admission** (`ssaunits.field_grow_root`): the append's receiver is
`record_get` of a record this frame holds as a parameter or owns outright (not
a projection of something else, not a closure environment); after the append,
every use of the record in its block is a `record_get` of a DIFFERENT field,
nothing anchored to the record is still live unless it reaches it through such
a projection, and the record is not live out of the block. The last clause is
what refuses a loop that reaches the same record again; a loop-carried record
is a fresh phi each round and admits. The plan records the record at the
append's result (`Plan.grows`, `grow_fields`).

**Lowering** (`ssarc.append_field`): the AST shape, spelled over values —

```
unique = __fern_rc_is_unique(record)
if (!unique) __fern_arr_share_inc(field)     # a shared record forces the copy
res = __fern_arr_push(field, v)              # non-consuming
if (!unique) __fern_arr_share_dec(field)
if (res == field) record.field = 0           # moved out: the result is its only name
else if (counted elements) __fern_arr_inc_elems(field)
```

The identity arm nulls the field, so the record's later release — the plan's
own drop, an AST caller's `__field_reclaim_<T>`, an `own` parameter's exit
release — finds nothing there; every rc helper skips a null. A reallocating
grow or a copy leaves the old buffer in the field for that release, its
elements aliased by the result and counted for the way `append_borrowed` counts
them.

**The caller side**, in both directions of the mixed module:

- A produced caller (`ssarc.bracketed`) holds a count on each field a callee
  may grow of a borrowed record argument it still reads, and on those fields
  alone — capturing each buffer in a scratch local so the release after the
  call decrements what was retained. The rows come from every produced plan
  (`ssaunits.grow_rows`), which is why `semlower` now plans every body before
  it lowers any.
- An AST caller reads the same rows through the registries it already reads:
  `irlower.regrow_sigs` reruns the may-grow fixpoint with each produced
  declaration's mask (`ssarc.grow_mask`) seeded in place of the one its syntax
  would give. Without that, a produced admission the AST walk refuses would be
  a grow no AST caller brackets.

## The second shape: the field handed on

With the append admitted, the 200-function input still copied 5.4 GB. The
assembler's hot site is not an append of its own —

```fern
a = X86Asm { ...a, code: x86_osz(a.code, size) };
```

— it hands the field to a callee that appends to its borrowed parameter, and
`bracketed` held a count on every borrowed array argument the frame did not
own, a projection included, so the callee copied every time.

The same admission answers it (`ssaunits.hands`): a call operand that is a
projection whose record this frame reads no further through that field, or a
borrowed array parameter of its own it reads no further, is handed on without
a bracket. The buffer's only later holder is the callee's result — retained on
identity, fresh on a realloc — and the record, or this frame's caller, releases
the old one. A handed FIELD is gated the way the append is: the record's own
count decides, and a shared record — one stored in a container this frame
built, say — holds a second count on the field across the call so the callee
copies (acc_via_shared in the RC fixture). The rows then close transitively (`ssaunits.grow_table`): a
function handing a buffer unbracketed to a callee whose row says that
position may grow carries the row at its own parameter and field, and
`semlower` runs the closure over every produced plan before lowering any.
That is the AST side's `grow_dying_passes`, over values.

## Measured

| program | AST | semantic before | semantic after |
|---|---|---|---|
| `emitop` reproducer, 40,000 pushes through a borrowed record | 60 ms | 11.1 s, 8.9 GB | 60 ms |
| the same through an `own` record, two pushes per call, 20,000 calls | 60 ms | 10.3 s, 8.9 GB | 60 ms |
| the field handed to an appending callee, twice per call, 20,000 calls | 60 ms | copies per call | 60 ms |

Under `FERN_SANITIZE=1` the keeping-caller program (a caller that reads its
record after the call, a nested chain, a loop, counted elements) answers the
same on both paths with allocs and frees balanced: 1,044 each on this branch,
where main's semantic build made 3,049 allocations for the same program and
the AST lowering leaks 928 bytes of its own.

The compiler built through the path, on the 200-declaration input and on
`lexer.fern` (the full table is in `SELFHOST-SEMANTIC-SOURCE.md` → "The
field append, admitted"): bytes copied through the cliff 5.43 GB → 1.50 GB
and 9.10 GB → 2.93 GB, peak 3.7 GB → 5.6 GB and 5.8 GB → 6.9 GB.

**The peak rising while the copies fall is churn, not a leak**, and it took
an instrumented build to say so: `FERN_LEAKCHECK=1` on the produced compiler
shows it leaving less live at exit than the AST-built one (22.5 MB against
67.0 MB; 29.6 MB against 142.5 MB) and freeing four times as much. Peak RSS
cannot distinguish the two; read it beside the leak line or not at all.

## What the measurement found that it was not looking for (#9407)

The produced compiler's output on `lexer.fern` differs from the AST-built
compiler's, and did before this change: main's own semantic build emits the
same 150 lines. Three zero-minus-one constants — the two
`var b: i32 = 0 - 1;` in `match_multipunct` and the `return -1;` in
`test_mixed` — come out as `xorl %eax, %eax; movq $o, %rcx; subq %rcx, %rax`
instead of `movq $-1, %rax`. The `$o` is the tell: a constant op's immediate is printed
from its `str` field, the literal's text, and `const_i32_readable` refuses
to fold text it cannot read — so the byte behind that `1` was reissued
memory. It was the constant folder's own literal, a view sliced out of a
string its frame then released
(`2026-09-15-a-lent-view-is-copied-for-a-callee-that-keeps-it.md`). The
200-declaration input's output is byte-identical, so the shape needs
something `lexer.fern` has. Every earlier
measurement of this compiler read peak and exit code; none compared the
output, and exit 0 with the wrong assembly is what that misses. Compare the
emitted text against the AST build's on every measurement from here on.

## Traps

**`record_fields` indexes unconditionally.** `f.records[find(...)]` is an
out-of-range read for a type the schema table does not hold — a scalar
parameter, say. Look a record up only where a row already names that position.

**A backtick inside a Go raw-string fixture ends the fixture.** The Fern
drivers in `internal/e2eselfhost` are Go raw strings; a comment quoting a name
in backticks splits the string at the wrong place and `go vet` reports it as a
Go syntax error at a line that looks like Fern.

**The keeping caller is the test, not the dying one.** The reproducer measures
the win; only a caller that still reads its record afterwards can show the
bracket missing, and only with counted elements does a buffer released under
two holders read back as a wrong name rather than a right one.
