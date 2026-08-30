# The `core/iter` adapters strand two boxes per element, and where the release is refused

`iter.filter` and `iter.map` were the leaks left standing after #7837 — the
"still not fixed" line in its body. This is what they are.

The short version: it is ONE missing release, not a second variant of the
projection bug #7837 fixed, and it is not confined to those two adapters. The
machinery to fix it already exists and refuses on a freshness test that answers
a stricter question than the reclaim actually needs.

A warning before any of the numbers, because it invalidated a whole earlier
pass at this: the heap header is not one size.

## The header is 8 bytes for boxes and 16 for arrays

This is the trap that produced the wrong draft, and it is worth stating first
because every `rctrace` analysis depends on it.

`__fern_alloc` returns the BLOCK. The object pointer is block + header, and the
header is NOT one size:

* `__fern_alloc_box` / `__fern_alloc_rc1` — `add rax, 8`. An 8-byte header
  carrying the rc word alone.
* `__alloc_u8` and the array paths — `lea rax, [rax + 16]`. A 16-byte header:
  cap at [data-12], rc at [data-8], len at [data-4].

`__fern_rc_is_unique` reads `[rdi-8]` in both cases, which is why one offset
looks like it works for everything until the pairing is checked against a
second allocation.

`TestRcTraceX86_64IncPointerIsTheObjectNotTheBlock` is correct as written — it
allocates a `u8[]`, which really is +16. It does not generalise, and this note
is what happens when it is assumed to.

The alloc/free pairing in the census is unaffected: `a` and `f` both carry the
block pointer, so the census's leak counts stand. Only the attribution of
inc/dec/is_unique events to an allocation needed the fix.

## Method

    import "core/iter" as iter;
    function main(): i32 {
        var xs: i32[] = [5, 2, 8, 1, 4, 9, 6];
        var it = iter.of(xs);
        var ys = iter.filter(it, function(x: i32): boolean { return x % 2 == 0; });
        return ys.len();
    }

Compiled with `FERN_RC_TRACE=1`, plus a temporary `u` event at
`__fern_rc_is_unique`'s entry (and `OpRcIsUnique`'s inline fast path routed
through the helper under `RcTrace`, since #4402 opt 2b otherwise inlines it and
the tracer never sees it). The instrumentation is REVERTED; it is not in the
tree. Every event pointer is then attributed to the block that contains it,
and the offset within that block is reported rather than assumed.

## Result: 18 allocations, 15 stranded, from one missing release

    blk       base    sz allocsite  hdr  events
      0 0x10000000    48 0x4000e5   16  LEAK  i x8, d x2
      1 0x10000030    32 0x400248    8  FREED i u d u f
      4 0x10000080    32 0x400874    8  LEAK  i u d
      6 0x100000c0    32 0x400874    8  LEAK  i u d       (and 9, 11, 13, 15, 17
                                                           — seven in total)
      3 0x10000060    32 0x4007e1    ?  LEAK  (no events at all)
      5 0x100000a0    32 0x4007e1    ?  LEAK  (no events at all)
                                              (and 8, 10, 12, 14, 16 — seven)

Sizes name the two: 0x14 = 20 = 8 header + 12 payload is the `ArrayIter`
struct (the drop passes 0xc = 12 as its size), and 0x18 = 24 = 8 + 16 is the
two-word Option/tuple box `next` returns.

### The source shape

    pub function filter[T, I: Iterator[T]](it: I, keep: (T) => boolean): T[] {
        var out: T[] = [];  var cur = it;  var go = true;
        while (go) {
            match (cur.next()) {
                Some(t) => { if (keep(t.0)) { out = out.append(t.0); }
                             cur = t.1; },
                None    => { go = false; },
            }
        }
        return out;
    }

`ArrayIter[T].next(self) -> Option[(T, Self)]` allocates two boxes per call,
and the sizes identify them exactly:

* 0x400874, 20 bytes = 8 header + 12 = `ArrayIter { xs: ptr, idx: i32 }`.
  The drop passes 0xc = 12 as its size, confirming it.
* 0x4007e1, 24 bytes = 8 header + 16 = the tuple `(T, Self)` the match binds
  to `t`.

### One root cause, not two

The tuple boxes (0x4007e1) carry NO rc traffic at all: no inc, no dec, no
`is_unique`, no free. Nothing ever attempts to release the match binding `t`.

That single omission explains every other number here:

* `t` holds field 1, the fresh `ArrayIter`. `cur = t.1` retains it, so the
  ArrayIter sits at rc 2: one reference from `cur`, one from the tuple that is
  never dropped.
* At the NEXT iteration's `cur = t.1` overwrite, the old `cur`'s drop runs,
  `is_unique` reads 2, returns 0, and the non-reclaiming `__fern_rc_dec` takes
  it to 1. Stranded at rc 1 — with exactly one outstanding reference, the one
  the tuple still holds. The `i u d` trace on all seven is precisely this.
* Block 0 is `xs` itself (7 i32 = 28 bytes + 16 header, rounded to 48; the
  16-byte header confirms an array). It reads 8 incs against 2 decs, and the 6
  unreleased retains are the 6 stranded ArrayIters each holding `xs`.

So the earlier framing of "C1 = a surplus inc" was the wrong way round: there
is no surplus retain. There is a missing RELEASE, and everything downstream is
one reference short of reclaimable. That distinction decides the repair — an
inc to remove is a different change from a drop to emit.

Block 1 is the control that shows the drop code itself is correct. It is the
box allocated BEFORE the loop, it is dropped twice (`i u d u f`), and the
second time rc has fallen to 1, `is_unique` returns 1, and it is freed by the
same branch that bails on all the others:

    test eax, eax
    je   .not_unique
    rdi = [obj]; rsi = 4;   call __fern_arr_dec    ; field 0, the array
    rdi = obj;   rsi = 0xc; call <free>            ; the box itself
    jmp  .done
  .not_unique:
    rdi = obj; call __fern_rc_dec                  ; reclaims nothing

### This is the whole combinator library, not one adapter

Every combinator in `core/iter` is written to the same shape — the iterator
protocol's shape, so any user-written iterator has it too:

    sum    Some(t) => { total = total + t.0; cur = t.1; }
    count  Some(t) => { n = n + 1;           cur = t.1; }
    fold   Some(t) => { acc = f(acc, t.0);   cur = t.1; }
    map    Some(t) => { out = out.append(f(t.0)); cur = t.1; }
    filter Some(t) => { if (keep(t.0)) { out = out.append(t.0); } cur = t.1; }
    take   Some(t) => { out = out.append(t.0); cur = t.1; k = k - 1; }

`cur = t.1` is what makes the binding escape in every one of them, so all six
are refused by the same gate and strand two boxes per element. That raises what
this repair is worth well past the two adapters the #7837 body names: it is the
per-element cost of iterating anything at all.

### Where the missing release is refused — measured, not inferred

`Option[(T, Self)]` is PAIR-FORM here: the trace shows two allocations per
iteration, not three, so the enum has no box of its own and the stranded
24-byte object is the tuple payload. That puts this on the pair-form path, and
the machinery for it already exists — `reclaimablePairFormPayload`
(internal/ir/rc_insert.go), whose own comment names this exact situation: "A
pair-form payload has no box behind it, so the reclaim the heap-form path gets
doesn't apply and a pointer-shaped payload is left ownerless."

Instrumented (temporarily; reverted) to print its four conditions, the arm
reports:

    PAIRFORM name=t callee=__method_iter__ArrayIter__i32_next
             ptr=true isPairForm=true confined=true fresh=FALSE
             | dunder=true noParamEscape=false isEnumType=true

Three of four pass. The binding IS confined to the arm. Only
`freshPairFormEnumResultType` refuses, and it refuses for TWO independent
reasons, which want opposite treatment:

**1. `strings.HasPrefix(id.Name, "__")` — redundant and wrong.** It is meant to
exclude builtins, but a concrete method call is mangled to `__method_*` by rc
time, so every user method is caught by it. The correct test for "user
function" is already available and already used elsewhere in this file:
membership in `returnsNoParamEscape`, whose map keys every decl in
`prog.Funcs` (`_, isUserFn := b.returnsNoParamEscape[name]`, ir.go:13280).
`__method_iter__ArrayIter__i32_next` IS a key. This one is a small fix on its
own terms, and it does not by itself unblock `filter`.

**2. `returnsNoParamEscape` is false — and that is CORRECT.** `next` returns
`Some((self.xs[i], ArrayIter { xs: self.xs, ... }))`, so the returned tuple's
field 1 holds the parameter's array. `findReturnsNoParamEscape` is a fixpoint
over "does the returned expression let a parameter's heap escape", and the
honest answer here is yes.

### The actual gap: one predicate answering two questions

The refusal above is right about what it measures and wrong about what the
reclaim needs. `returnsNoParamEscape` asks

> does anything REACHABLE FROM the result alias a parameter?

whereas releasing the tuple buffer needs only

> is the result POINTER ITSELF freshly allocated?

`ArrayIter.next` answers no to the first and yes to the second: the tuple box
is allocated fresh at rc 1 on every call, and what it CONTAINS aliases the
caller's array. A deep drop needs both properties; a shallow release of the
buffer needs only the second, because the contents are handed to the bindings.

So the repair is to split the predicate and give this reclaim a SHALLOW
verdict — which is the shape `reclaimableMapGetScrutinee` next door already
has ("The plan's `counted` half decides SHALLOW versus DEEP"), and which
`emitOwnedConsumingArmDrop` already implements:

    unique  -> free the box BUFFER only; the bindings inherit the payload counts
    shared  -> inc the counted bindings, then flat-dec our box reference

Two neighbouring reclaims disagreeing on whether the shallow/deep distinction
exists is the tell that this is a missing case rather than a new concept.

Note how this composes with #7837. Now that `cur = t.1` credits the projection
as owned, it emits an inc, so field 1 is retained by `cur` before the release
runs. That is what makes the shallow release safe rather than merely
convenient — and it is why this repair was not available before that landed.

### The prediction this makes

If the tuple payload is released, the 14 stranded BOXES should free and the
source array (the fifteenth stranded allocation, and the only one that is not
a box) should see its retains balance and `xs`'s inc/dec should balance. That is a single measurable outcome,
and it is how the repair should be judged — not by the conformance fixtures,
which did not move for the last leak fix that worked.

Note the neighbouring risk: `markMatchBindingAliasMoves`, an earlier attempt to
sweep match bindings, SEGFAULTED `unidiff.fern`. "A binding is swept by nobody"
is false for some shapes, so a repair here must establish WHICH bindings are
already swept before adding a drop, or it will double-free rather than leak.

## The wrong answer this produced first, kept as a warning

Reading the boxes at +16 does not fail loudly. It silently points 8 bytes past
every object, so the `u` events land on addresses that belong to no allocation
and the natural conclusion is "`__fern_rc_is_unique` is never called on these
blocks, so the drop is never reached". That is what the first pass concluded,
and it is false in both halves: the drop is reached on every iteration.

Worth noting which part of that pass held up. The filtered event stream could
not distinguish "never reached" from "reached with some other pointer", and
declining to claim the stronger version was correct — the stronger version was
the false one. The instrument was wrong; the discipline about what it could
support was not.

## Fixed here on the way

`emitAllocU8Runtime`'s doc comment said "Returns the data pointer (header + 8)"
directly above `lea rax, [rax + 16]` and a `[rax - 12]` cap store — stale, and
stating exactly the thing that produced the wrong answer above. Corrected in
this change, along with the `RcTrace` doc, which described the offset as though
there were one of them.
