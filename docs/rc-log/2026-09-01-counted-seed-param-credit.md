# The threaded-walker seed (#7914 frontier, the module tables fall)

The reverse-reachability census that named this is worth keeping as a
method. Instead of ranking survivors by their ALLOCATION site — which
says where bytes were born, not why they are still alive — build the
holder graph over the survivor set (every 8-byte word inside a survivor
that lands inside another survivor is an edge), take the blocks with no
in-edge as ROOTS, and rank each root by the bytes reachable ONLY from
it. That is the number a fix is worth.

On the driver at 369,648 B retained: 1,929 roots holding 85,904 B
between them, and one root at the top —

```
root 10033740 size=128 site=checker__new_scope_full
    reach=539/192,304B  excl=42/87,584B
         4 blk    57,408 B  checker__sig_table
         1 blk    28,672 B  checker__struct_table
```

**One orphaned 128-byte Scope box exclusively held 87,584 B of module
tables** — 24% of everything the compiler retained, and the whole of
class A, which two earlier passes had chased through the tables
themselves.

## The Scope came from an argument temp

```
var ntop: ast.Stmt[] = annotate_block(mod.top_stmts,
    new_scope_full(mt.sigs, mt.structs, mt.unions, mt.methods, mt.imports));
```

A fresh Scope, never bound to a local, handed straight to a callee.
Nothing releases it unless the callee's parameter is counted-retain, and
`paramCountedRetain[checker__annotate_block]` read `s(Scope)=false`.

## One occurrence, and it was already known to be counted

A `FERN_DBG_WHY` dump named the single uncredited occurrence:

```
DBGWHY checker__annotate_block param=s total=1 safe=0 uncredited at: 13911:20
```

which is

```
function annotate_block(body: ast.Stmt[], s: Scope): ast.Stmt[] {
  var cur: Scope = s;              // 13911:20
  for stmt in body { … cur = annotate_advance(stmt, cur); }
```

`var cur = s` reads as "the callee bound the parameter to a local", the
canonical uncounted retention — except that it is not one here.
`computeFreeEligible` has said so since #6403: its `countedSeed` map
exempts exactly this binding, because for a local that is REASSIGNED
later the `*ast.Var` lowering emits the transfer inc, so the local owns a
reference of its own. The counted-retain summaries had no arm for it, so
the fact the escape analysis already knew was invisible to the tier that
decides whether the CALLER may release its argument.

Same shape, same refusal: `check_block`'s `var cur: Scope = s`, and
through the fixpoint every walker downstream of them.

## The fix

`countedSeedOccurrences(fn)` marks the parameter occurrences that seed a
locally-declared, later-reassigned binding, and all three tiers
(`stringParamCounted`, `arrayParamCounted`, `paramProjectionsSafe`)
credit them. Both of `countedSeed`'s conditions carry over unchanged:

- **reassigned** — a binding that only ever holds the seed gets no
  transfer inc and is a genuine borrow;
- **declared once** — `localNameUnique`, spelled as a declaration count
  because these summaries run before the builder's slot map exists.

## Measured

**Driver: 369,648 → 281,632 B (−88,016, −23.8%, +418 frees)**, output
byte-identical. Across the three credits of this arc: 478,992 → 281,632,
**−41%**.

## No reduced probe reproduces it, and that is worth recording

Five probes were built at increasing fidelity — a threaded scope, one
returning an array, one with a nested table field, one with mutual
recursion back through the walker, one with the call moved out of `main`
— and every one is `allocs == frees, live_bytes 0` on BOTH sides of the
change, on both natives. The argument-temp reclaim already covers those
shapes by another route (the consumed-param promotion), so the credit is
inert in them. The evidence for this change at runtime is the driver
measurement itself; the IR-level unit tests are what pin the rule, and
they were checked non-vacuous against the change reverted.
