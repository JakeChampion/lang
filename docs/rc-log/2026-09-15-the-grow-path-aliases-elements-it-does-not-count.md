# The grow path aliases elements it does not count, and a leak was load-bearing

#9365. An append through a borrowed parameter copies the whole buffer per
element on the semantic path: 40,000 pushes take 23 s and 8.9 GB where the AST
lowering takes 2 ms. Eight lines reproduce it:

```fern
function push(buf: i32[], v: i32): i32[] { return buf.append(v); }
function build(n: i32): i32[] {
    var out: i32[] = [];
    var i: i32 = 0;
    while (i < n) { out = push(out, i); i = i + 1; }
    return out;
}
function main(): i32 { return build(40000).len() % 97; }
```

The mechanism was clear early. `ssarc.sole_owned_base` asks
`__fern_rc_is_unique` to choose between growing in place and un-sharing, but
`supplies` has already emitted the receiver's `__fern_rc_inc` before
`instruction` runs. The test reads the count its own supply added, answers
"shared" about a box nobody else holds, and takes the copy every time.

What took three attempts was the fix.

## Attempt 1: move the retain into the unique arm

`push` then grows through `__fern_arr_push_owned` at rc 2, which re-gates and
copies anyway — 3.5 s and 6.4 GB. The same reordering applied to `with` is a
miscompile on its own, because `arr_set` mutates unconditionally.

## Attempt 2: copy the AST lowering (#9388, reverted in #9392)

Hold the retain back, push through the non-consuming `__fern_arr_push`, retain
the result when the pointer comes back unchanged, and add the caller-side
may-grow bracket of #4873 so a caller that still reads the array forces the
copy.

Reproducer: 2 ms. It also corrupts arrays. One appender, heap elements, a
caller that rebinds:

```fern
struct Box { name: string }
function add(acc: Box[], n: string): Box[] { return acc.append(Box { name: n }); }
function build(n: i32): Box[] {
    var out: Box[] = [];
    var i: i32 = 0;
    while (i < n) { out = add(out, tag(i)); i = i + 1; }
    return out;
}
```

| build | output |
|---|---|
| AST | `aa bb cc dd ee ff gg hh ii` |
| semantic, with the deferral | `ii hh gg ff ee ff gg hh ii` |
| semantic, under `FERN_SANITIZE` | correct, then `use-after-free (touched a quarantined block)` |

The sanitizer answering CORRECTLY is the tell: it quarantines rather than
recycling, so freed blocks are not handed back out. The corruption is the
freelist reissuing a block something still points at.

### What the runtime actually promises

```
// LOAD-BEARING LEAK — do NOT add a free of the old buffer (%r12) here.
```
— `asm_ir.fern`, in `__fern_arr_push`'s grow path.

`__fern_arr_push` frees nothing on any path. Its grow path allocates a fresh
box, memcpy's the element POINTERS into it **without retaining them**, and
abandons the old buffer. The two then share one count per element.

The AST lowering settles that by never releasing the old buffer, which is why
the AST build leaks 524 KB on the reproducer. The semantic path's ledger
balances every unit, so its caller does release it — and the aliased elements
die under the box that now points at them.

## Attempt 3: make it a MODE

`__fern_arr_push_owned` reclaims correctly: on a grow it hands the superseded
box to the freelist and leaves the elements alone, because they moved. It
consumes a unit, and a borrowed parameter has none — so infer the mode instead.
A reference parameter appearing as an `append` or `with` receiver becomes
counted; the caller then moves its unit in when it is done with the array and
retains when it is not.

Every reproducer passes, and the compiler self-builds. It also segfaults on
almost every module it compiles.

`ssarc.caller_sigs` reads a counted parameter as "the caller moved its unit
in". For a declared `own` position the checker guarantees that:

```
error[E051]: argument to owned parameter must be an owned value
             (a fresh construction or another `own` parameter), not a borrowed one
```

An INFERRED counted parameter has no such guarantee. An AST-lowered caller
passing a borrowed value supplies nothing, the produced callee releases what it
was never given, and `__rc_underflow_count()` is non-zero — which the semsource
RC fixture asserts, and which reaches a segfault in the compiler. One step
reproduces it:

```
FERN_SEM_IR=1 FERN_SEM_IR_SKIP=caller   # the caller keeps the AST lowering
```

So a mode is not free to be inferred: it is half of a contract whose other half
the checker enforces on source the user wrote.

## What landed

Attempt 2 plus its missing half. The grown arm counts the elements it aliased:

```
call __fern_arr_push
cmp result, recv
je  .Lsame
  call __fern_arr_inc_elems(recv)     # the boxes the fresh one aliases
jmp .Ldone
.Lsame:
  call __fern_rc_inc(result)
```

`__fern_arr_inc_elems` over the RECEIVER names exactly the aliased boxes: the
receiver is untouched by the push, so the element just pushed is not among them.
O(n) per grow, amortised O(1) against the doubling, and the same shape
`sole_owned_base` already uses on its copy arm.

x86-64 reproducer: 2 ms, allocs and frees balanced.

## What it does NOT fix, measured

The compiler's own peak on `lexer.fern`, built through the semantic path:

| build | peak | exit |
|---|---|---|
| no deferral | 11,550 MB | 0 |
| deferral + the element count | 10,726 MB | 0 |

7%. The borrowed-PARAMETER append is a real quadratic and is fixed, but it is
not what #9365's 11.5 GB is made of, and the issue's reading of its own bisect
— that the `w`-`z` cone means `x86_emit_mem(buf: i32[], …)` — does not survive
reading the code: `asmcore.EmitState.write` routes through a global strbuf and
appends to no array at all.

The 18 MB that #9388 reported for this same lowering is an artefact. That run
exited 139, and this one 134: both crashed partway, so the peak is the peak up
to the crash and not a figure for completed work. **A peak from a run that did
not finish is not a measurement** — the only two comparable numbers are the two
above, both exit 0.

## Traps this set

**The sanitizer changes the answer.** A freelist-reissue bug reads as correct
under `FERN_SANITIZE` and wrong under `FERN_LEAKCHECK`. Run both: agreement is
the signal, and a program that only misbehaves without the sanitizer is a
reissue rather than an overrun.

**Balanced allocs and frees do not mean correct.** Every run in attempt 2
reports `allocs=N frees=N live_bytes=0`, the corrupting ones included. The count
balances precisely because the extra release is paired with an allocation that
reuses the block.

**A leak can be load-bearing.** The instinct on reading the AST path's 524 KB
was to treat it as a defect the semantic path improves on. It is the invariant
`__fern_arr_push` is written against, and the semantic path's better number is
what broke it.

**Peak memory hid a second bug.** Attempt 2's 18 MB looked like the finish line;
the produced compiler was segfaulting on almost every input at the same time.
Measure the ANSWER alongside the number, on every module and not just the one
the bisect named.

**`FERN_SEM_IR_SKIP` is a boundary test, not only a bisect knob.** Skipping the
CALLER puts a produced callee behind an AST-lowered caller in one step, which is
the mixed-module contract every change to `caller_sigs` or to a parameter mode
has to hold.
