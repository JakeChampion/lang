# the cell store releases what it superseded, not what it stored (#8833)

#8441 made the boxcapture cell's `x[0] = v` store counted: retain an
alias-shaped new value, then release the element it supersedes through
`dropStructField`. The release was unconditional, and a consuming update that
finds its receiver uniquely held mutates IN PLACE and hands the SAME reference
back — so the store released the very value it was about to store.

```fern
import "core/map";
function main(): i32 {
  var m: Map[string, i32] = map_new(2);
  var f: () => i32 = (): i32 => { return m.len(); };   // captures m, so m is boxed
  m = m.insert("k", 1);
  return m.len() * 10 + f();                            // expect 11
}
```

| engine | before | after |
| --- | --- | --- |
| `-interp` (oracle) | 11 | 11 |
| x86-64-linux | **0** | 11 |
| arm64-linux (qemu) | **0** | 11 |
| wasm32-wasi (node WASI) | 11 | 11 |
| x86-64-linux `-sanitize` | `use-after-free (touched a quarantined block)`, 124 | 11, `leak 144` |
| arm64-linux `-sanitize` | `use-after-free`, 124 | 11, `leak 160` |

The residual leak is #8845, not this store — see "The pin is not this store".

## Why the retain half could not cover it

The right-hand side is an `*ast.Call`, and `needsRcIncOnAlias` declines a call
for the ordinary reason that a call result is owned. Here it is not: a Map read
out of the cell is BORROWED (no inc at the read), `__map_cow_inplace` finds
rc==1, mutates and returns its argument, so the expression yields the cell's own
reference with no count on top of it. Releasing it took the map from 1 to 0.

The string spelling escapes for that reason and not by luck: a string element
read out of a cell IS retained, so `s = s + piece` and `s = mk(i)` arrive
owning a reference and the release is owed. Measured, both clean before and
after: `s = s + "gh"` reads 44, `s = id(s)` 99, `a = a.with(0, 7)` 77, each
`allocs == frees`. The cow-in-place Map mutator is the shape where the two
halves disagree.

## The guard, and why it is narrow

`emitBoxedCellStore` stores first and releases second, under the identity test
the plain-local Map overwrite already uses — but ONLY for the one RHS shape
that can hand a borrowed reference back: `isCellSelfMapCow`, a
`__method_Map_set` / `__method_Map_clear` call whose receiver is a read of this
cell. `isSelfMapMutation` asks the same question of a plain local and matches
an `*ast.Ident` receiver, which BoxMutatedCaptures has already rewritten into
`m[0]`, so the two predicates cannot be shared.

The old element is stashed twice before the value expression runs — the whole
payload, which the release consumes, and its first word, which the guard
compares against the word the store left in the slot. One word suffices on a
two-word string ABI: the data word is the buffer's identity, and an inline /
immortal payload releases to a no-op on either arm.

**A guard on every shape is wrong**, and this was measured rather than
reasoned. Every other RHS delivers an OWNED reference on both paths, so
skipping the release when the pointer happens to match strands one value per
rebind. Each row is a 50-round loop under a live capture, `-sanitize` on
x86-64, `leak` bytes:

| RHS shape | pre-#8441 raw store | `main` (#8441) | guard on EVERY shape | guard on the cow shape only |
| --- | --- | --- | --- | --- |
| `m = m.insert("k", i)` | 144 | 144 | 144 | 144 |
| `m = map_new(4); m = m.insert("k", i)` | **7344** | **124 use-after-free** | 144 | 144 |
| `m = id(m)` (`@noinline` identity fn) | **7344** | 144 | **7344** | 144 |
| `a = a.append(i)` | **6032** | clean | **6000** | clean |
| `var t = id(m); m = t` | **7344** | 144 | **7344** | 144 |
| `a = a.with(0, i)` | **2400** | clean | clean | clean |
| `s = s + "abcdefgh"` | **10784** | clean | clean | clean |
| `s = mk(i)` | **2352** | clean | clean | clean |
| `m = map_new(4)` | **7344** | 144 | 144 | 144 |

The first column is the reason a revert is not the fix: dropping the release
restores the repro's 11, and strands a generation per rebind in every other
row. The last column is the only one with no bold entry.

`m = id(m)` is the row that killed the wider guard, and it is worth naming why:
the callee takes the return-transfer inc, so its result IS owned even though
the pointer is unchanged, and `.append`'s in-place grow likewise pairs with the
buffer dec the store emits. The wider guard was reached for on the theory that
"no self-mutation predicate would ever admit `m = id(m)`" — true, and
irrelevant: that shape never needed admitting, because #8441 already handles it
correctly.

## Both arms are load-bearing

The not-equal arm is #8441: superseding a genuinely different map must still
release the old one. `closure_capture_rebind_map_in_place_not_over_released`
runs both in one loop — `m = map_new(4)` (different handle, released) then
`m = m.insert("k", i)` (same handle, not released) — and without the release it
strands 50 maps instead of one. Its three former states are all witnessed:
exit 124 on both natives before this change, 30752 leaked bytes on wasm (which
has no use-after-free detector, so the miscompile showed only in the census),
and 144 / 160 / 128 after.

Non-vacuity, run by snapshotting `internal/ir/ir.go` to a copy and restoring
from that copy (never `git checkout`, which would discard the change under
test):

| mutation | corpus case | differential row | leak gate |
| --- | --- | --- | --- |
| guard reverted, release unconditional for every shape (= #8441) | **FAIL** — x86-64 exit -1, arm64 `uncaught target signal 11` | **FAIL** — `x86_64 = "0", interp = "11"` | pass |
| release deleted entirely (= pre-#8441 raw store) | pass | pass | **FAIL** — `closure_capture_rebind_churn_free` 15968 B and `closure_capture_rebind_alias_not_over_released` 16032 B against a pinned 0, this case 7344 / 8144 against its pinned 144 / 160 |
| guard applied to every RHS shape | pass | pass | **FAIL** — `closure_capture_rebind_identity_call_not_stranded` 7344 B against its pinned 144, `closure_capture_rebind_append_in_place_still_released` 6000 B against a pinned 0 |

Three mutations, three disjoint sets of red gates, and only the shipped store
passes all of them. The second row is what separates this change from a revert
of #8441. The third is why the last two corpus cases exist: nothing in the
corpus covered a captured rebind whose RHS returns the same pointer *owning* a
reference, so the wide guard was green everywhere and nearly shipped — the same
hole, one shape over, that let #8441 land.

## Still broken: a moved alias of the cell's own element

```fern
var t: Map[string, i32] = m.insert("k", 1);   // cow in place: t IS m's handle
m = t;                                        // over-release, exit 124
```

unchanged by this work — 124 on `main` and 124 after. The cause is upstream of
the store: the `var` binding takes a borrowed cow-in-place result without
retaining it, so `t` and the cell share one count between them and the store's
release (correctly, for a moved alias that owns its reference) spends it. The
store cannot tell this apart from `var t = id(m); m = t`, where `t` genuinely
owns a count and the release is owed — the two differ only in `t`'s
initialiser, and the pointer is the same on both. Filed as #8853; fixing it at
the binding is the coherent place, not by widening this guard.

## The pin is not this store

That 144 / 160 / 128 is the one map the cell still holds at exit. A cell is a
one-element array and the array element ladder flat-dec's a Map handle, freeing
neither its columns nor its buf — #8845, filed with this. Independent of the
store: `m = map_new(4)` under a capture leaks the identical 144 on `253e39f`
and with this change applied, and a plain `Map[string, i32][]` of two maps
leaks all 288 bytes of both on either compiler. #8441's "Next lead" named this
arm; it is now a numbered gap with a case pinned on it rather than a note.

## Trap

`-sanitize` on wasm carries the leak census ONLY — no use-after-free detector,
no over-release counter. This bug read exit 11 there throughout, on the
compiler that miscompiled it on both natives. A silent wasm run means "did not
leak", never "correct".

## Measuring wasm without wasmtime

`-emit command-module` gives a `_start` export that node's `node:wasi` runs
directly, and the census prints at the exit seam — so the wasm column above and
the pinned 128 are measured, not inferred. The harness reproduces
`closure_cycle_leaks_without_crashing`'s pinned 4000 exactly, which is what
makes its other numbers comparable. `-emit core-module` does not work for this:
no `_start`, so nothing reaches the exit seam and no census is printed.
